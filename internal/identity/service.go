// Package identity owns local provisioning and tenant authorization.
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/currency"
	"golang.org/x/text/language"

	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/database/sqlgen"
	"github.com/zeithrold/ledger/internal/problem"
)

// Service operates on an explicitly provided database, never a global connection.
type Service struct{ db *database.DB }

// New binds identity operations to a database.
func New(db *database.DB) *Service { return &Service{db: db} }

// User is the public internal-user representation.
type User struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
}

// Tenant is the authorized personal tenant.
type (
	Tenant struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
		Role string    `json:"role"`
	}
	// Book is a tenant-scoped ledger, with no financial data in phase 1.
	Book struct {
		ID           uuid.UUID `json:"id"`
		TenantID     uuid.UUID `json:"tenant_id"`
		Name         string    `json:"name"`
		BaseCurrency string    `json:"base_currency"`
	}
)

// Preferences stores display choices independently of book valuation currency.
type (
	Preferences struct {
		Locale   string `json:"locale"`
		Timezone string `json:"timezone"`
		Theme    string `json:"theme"`
	}
	// Context is the current locally authorized identity and personal space.
	Context struct {
		User         User        `json:"user"`
		InstanceRole string      `json:"instance_role"`
		Tenant       Tenant      `json:"tenant"`
		DefaultBook  Book        `json:"default_book"`
		Preferences  Preferences `json:"preferences"`
	}
)

// BootstrapInput is required only when creating a new identity.
type (
	BootstrapInput struct {
		BaseCurrency string `json:"base_currency"`
		Timezone     string `json:"timezone"`
		Locale       string `json:"locale"`
	}
	// PreferencesPatch updates only provided fields.
	PreferencesPatch struct {
		Locale   *string `json:"locale"`
		Timezone *string `json:"timezone"`
		Theme    *string `json:"theme"`
	}
	// StatusInput is an audited administrative command.
	StatusInput struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	// Instance is the immutable administrator assignment.
	Instance struct {
		AdminUserID   uuid.UUID `json:"admin_user_id"`
		InitializedAt time.Time `json:"initialized_at"`
	}
	// UserPage uses an exclusive UUID cursor.
	UserPage struct {
		Users      []User     `json:"users"`
		NextCursor *uuid.UUID `json:"next_cursor"`
	}
)

func pgID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
func invalid(name, reason string) error {
	return &problem.Error{Kind: problem.InvalidRequest, Detail: "Request parameters are invalid.", Fields: []problem.FieldError{{Location: "body", Name: name, Reason: reason}}}
}

func validateLocale(v string) error {
	if v == "" || len(v) > 100 {
		return invalid("locale", "A BCP 47 language tag is required.")
	}
	if _, err := language.Parse(v); err != nil {
		return invalid("locale", "A BCP 47 language tag is required.")
	}
	return nil
}

func validateTimezone(v string) error {
	if v == "" || v == "Local" {
		return invalid("timezone", "An IANA timezone is required.")
	}
	if _, err := time.LoadLocation(v); err != nil {
		return invalid("timezone", "An IANA timezone is required.")
	}
	return nil
}

func (in BootstrapInput) validate() error {
	if len(in.BaseCurrency) != 3 || strings.ToUpper(in.BaseCurrency) != in.BaseCurrency {
		return invalid("base_currency", "An uppercase ISO 4217 currency code is required.")
	}
	if _, err := currency.ParseISO(in.BaseCurrency); err != nil {
		return invalid("base_currency", "An ISO 4217 currency code is required.")
	}
	if err := validateTimezone(in.Timezone); err != nil {
		return err
	}
	return validateLocale(in.Locale)
}

func loadContext(ctx context.Context, q *sqlgen.Queries, id pgtype.UUID) (Context, error) {
	r, err := q.GetUserContext(ctx, id)
	if err != nil {
		return Context{}, fmt.Errorf("load local identity: %w", err)
	}
	if r.Status != "active" {
		return Context{}, problem.New(problem.UserDisabled, "This user is disabled.")
	}
	if r.TenantStatus != "active" || r.MemberStatus != "active" {
		return Context{}, problem.New(problem.AccessDenied, "The personal space is unavailable to this user.")
	}
	role := "user"
	if r.IsAdmin {
		role = "admin"
	}
	return Context{
		User: User{uuid.UUID(r.ID.Bytes), r.DisplayName, r.Status}, InstanceRole: role,
		Tenant:      Tenant{uuid.UUID(r.TenantID.Bytes), r.TenantName, r.Role},
		DefaultBook: Book{uuid.UUID(r.DefaultBookID.Bytes), uuid.UUID(r.TenantID.Bytes), r.BookName, r.BaseCurrency},
		Preferences: Preferences{r.Locale, r.Timezone, r.Theme},
	}, nil
}

// Current resolves a verified identity and checks all local authorization states.
func (s *Service) Current(ctx context.Context, who auth.Identity) (Context, error) {
	id, err := s.db.Queries.FindIdentity(ctx, sqlgen.FindIdentityParams{Issuer: who.Issuer, Subject: who.Subject})
	if errors.Is(err, pgx.ErrNoRows) {
		return Context{}, problem.New(problem.BootstrapRequired, "Initialize your personal space first.")
	}
	if err != nil {
		return Context{}, err
	}
	return loadContext(ctx, s.db.Queries, id)
}

// Bootstrap atomically claims the instance and provisions one personal space.
func (s *Service) Bootstrap(ctx context.Context, who auth.Identity, in BootstrapInput) (Context, bool, error) {
	var result Context
	created := false
	err := pgx.BeginFunc(ctx, s.db.Pool, func(tx pgx.Tx) error {
		q := s.db.Queries.WithTx(tx)
		state, err := q.LockInstance(ctx)
		if err != nil {
			return err
		}
		id, err := q.FindIdentity(ctx, sqlgen.FindIdentityParams{Issuer: who.Issuer, Subject: who.Subject})
		if err == nil {
			result, err = loadContext(ctx, q, id)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err = in.validate(); err != nil {
			return err
		}
		userID, tenantID, bookID := pgID(uuid.New()), pgID(uuid.New()), pgID(uuid.New())
		if err = q.CreateUser(ctx, userID); err != nil {
			return err
		}
		if err = q.CreateIdentity(ctx, sqlgen.CreateIdentityParams{Issuer: who.Issuer, Subject: who.Subject, UserID: userID}); err != nil {
			return err
		}
		if err = q.CreateTenant(ctx, tenantID); err != nil {
			return err
		}
		if err = q.CreateOwner(ctx, sqlgen.CreateOwnerParams{TenantID: tenantID, UserID: userID}); err != nil {
			return err
		}
		if err = q.CreateBook(ctx, sqlgen.CreateBookParams{ID: bookID, TenantID: tenantID, BaseCurrency: in.BaseCurrency}); err != nil {
			return err
		}
		if err = q.BindPersonalTenant(ctx, sqlgen.BindPersonalTenantParams{UserID: userID, TenantID: tenantID, DefaultBookID: bookID}); err != nil {
			return err
		}
		if err = q.CreatePreferences(ctx, sqlgen.CreatePreferencesParams{UserID: userID, Locale: in.Locale, Timezone: in.Timezone}); err != nil {
			return err
		}
		if !state.AdminUserID.Valid {
			if err = q.ClaimInstance(ctx, userID); err != nil {
				return err
			}
		}
		result, err = loadContext(ctx, q, userID)
		created = err == nil
		return err
	})
	if err != nil {
		return Context{}, false, err
	}
	return result, created, nil
}

func bookDTO(b sqlgen.Book) Book {
	return Book{uuid.UUID(b.ID.Bytes), uuid.UUID(b.TenantID.Bytes), b.Name, b.BaseCurrency}
}

// Books lists only the authorized personal tenant's books.
func (s *Service) Books(ctx context.Context, actor Context) ([]Book, error) {
	rows, err := s.db.Queries.ListBooks(ctx, pgID(actor.Tenant.ID))
	if err != nil {
		return nil, err
	}
	result := make([]Book, 0, len(rows))
	for _, b := range rows {
		result = append(result, bookDTO(b))
	}
	return result, nil
}

// Book hides whether a foreign tenant's identifier exists.
func (s *Service) Book(ctx context.Context, actor Context, id uuid.UUID) (Book, error) {
	row, err := s.db.Queries.GetBook(ctx, sqlgen.GetBookParams{TenantID: pgID(actor.Tenant.ID), ID: pgID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Book{}, problem.New(problem.NotFound, "The book was not found.")
	}
	return bookDTO(row), err
}

// UpdatePreferences applies a partial update without changing book currency.
func (s *Service) UpdatePreferences(ctx context.Context, actor Context, in PreferencesPatch) (Preferences, error) {
	if in.Locale == nil && in.Timezone == nil && in.Theme == nil {
		return Preferences{}, invalid("body", "Provide at least one preference.")
	}
	if in.Locale != nil {
		if err := validateLocale(*in.Locale); err != nil {
			return Preferences{}, err
		}
	}
	if in.Timezone != nil {
		if err := validateTimezone(*in.Timezone); err != nil {
			return Preferences{}, err
		}
	}
	if in.Theme != nil && *in.Theme != "system" && *in.Theme != "light" && *in.Theme != "dark" {
		return Preferences{}, invalid("theme", "Use system, light or dark.")
	}
	text := func(p *string) pgtype.Text {
		if p == nil {
			return pgtype.Text{}
		}
		return pgtype.Text{String: *p, Valid: true}
	}
	r, err := s.db.Queries.UpdatePreferences(ctx, sqlgen.UpdatePreferencesParams{UserID: pgID(actor.User.ID), Locale: text(in.Locale), Timezone: text(in.Timezone), Theme: text(in.Theme)})
	return Preferences{r.Locale, r.Timezone, r.Theme}, err
}

func requireAdmin(actor Context) error {
	if actor.InstanceRole != "admin" {
		return problem.New(problem.AccessDenied, "Instance administrator access is required.")
	}
	return nil
}

// Instance returns administrator metadata, never another tenant's data.
func (s *Service) Instance(ctx context.Context, actor Context) (Instance, error) {
	if err := requireAdmin(actor); err != nil {
		return Instance{}, err
	}
	r, err := s.db.Queries.GetInstance(ctx)
	return Instance{uuid.UUID(r.AdminUserID.Bytes), r.InitializedAt.Time}, err
}

// Users lists internal users in a stable exclusive UUID order.
func (s *Service) Users(ctx context.Context, actor Context, after *uuid.UUID, limit int32) (UserPage, error) {
	if err := requireAdmin(actor); err != nil {
		return UserPage{}, err
	}
	if limit < 1 || limit > 100 {
		return UserPage{}, invalid("limit", "Use a limit between 1 and 100.")
	}
	cursor := pgtype.UUID{}
	if after != nil {
		cursor = pgID(*after)
	}
	rows, err := s.db.Queries.ListUsers(ctx, sqlgen.ListUsersParams{Limit: limit + 1, AfterID: cursor})
	if err != nil {
		return UserPage{}, err
	}
	page := UserPage{Users: []User{}}
	if len(rows) > int(limit) {
		rows = rows[:limit]
		last := uuid.UUID(rows[len(rows)-1].ID.Bytes)
		page.NextCursor = &last
	}
	for _, r := range rows {
		page.Users = append(page.Users, User{uuid.UUID(r.ID.Bytes), r.DisplayName, r.Status})
	}
	return page, nil
}

// SetStatus changes a normal user and writes its audit event in the same transaction.
func (s *Service) SetStatus(ctx context.Context, actor Context, id uuid.UUID, in StatusInput) (User, error) {
	if err := requireAdmin(actor); err != nil {
		return User{}, err
	}
	if in.Status != "active" && in.Status != "disabled" {
		return User{}, invalid("status", "Use active or disabled.")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len(reason) > 1000 {
		return User{}, invalid("reason", "Provide a reason of at most 1000 bytes.")
	}
	var result User
	err := pgx.BeginFunc(ctx, s.db.Pool, func(tx pgx.Tx) error {
		q := s.db.Queries.WithTx(tx)
		state, err := q.GetInstance(ctx)
		if err != nil {
			return err
		}
		if state.AdminUserID.Bytes == [16]byte(id) {
			return problem.New(problem.AccessDenied, "The instance administrator cannot be changed.")
		}
		target, err := q.LockUser(ctx, pgID(id))
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.New(problem.NotFound, "The user was not found.")
		}
		if err != nil {
			return err
		}
		if err = q.UpdateUserStatus(ctx, sqlgen.UpdateUserStatusParams{ID: target.ID, Status: in.Status}); err != nil {
			return err
		}
		if err = q.CreateAdminAudit(ctx, sqlgen.CreateAdminAuditParams{ID: pgID(uuid.New()), ActorID: pgID(actor.User.ID), TargetUserID: target.ID, PreviousStatus: target.Status, NewStatus: in.Status, Reason: reason}); err != nil {
			return err
		}
		result = User{id, target.DisplayName, in.Status}
		return nil
	})
	return result, err
}
