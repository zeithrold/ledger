package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/zeithrold/ledger/internal/accounting"
	"github.com/zeithrold/ledger/internal/apiv1"
	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/observability"
	"github.com/zeithrold/ledger/internal/problem"
	"github.com/zeithrold/ledger/internal/problemhttp"
	"github.com/zeithrold/ledger/internal/rates"
)

// Backend is the local identity and authorization service boundary.
type Backend interface {
	Current(context.Context, auth.Identity) (identity.Context, error)
	Bootstrap(context.Context, auth.Identity, identity.BootstrapInput) (identity.Context, bool, error)
	Books(context.Context, identity.Context) ([]identity.Book, error)
	Book(context.Context, identity.Context, uuid.UUID) (identity.Book, error)
	UpdatePreferences(context.Context, identity.Context, identity.PreferencesPatch) (identity.Preferences, error)
	Instance(context.Context, identity.Context) (identity.Instance, error)
	Users(context.Context, identity.Context, *uuid.UUID, int32) (identity.UserPage, error)
	SetStatus(context.Context, identity.Context, uuid.UUID, identity.StatusInput) (identity.User, error)
}

// Dependencies enables business endpoints; nil dependencies keep HTTP-only mode.
type (
	Dependencies struct {
		DocsEnabled bool
		Telemetry   *observability.Runtime
		Backend     Backend
		Accounting  *accounting.Service
		Rates       *rates.Service
		Verifier    auth.Verifier
	}
	api struct {
		deps Dependencies
		db   Pinger
	}
)

func respondError(c *gin.Context, err error) {
	var p *problem.Error
	if !errors.As(err, &p) {
		p = problem.New(problem.Internal, "The request could not be completed.")
		p.Cause = err
	}
	problemhttp.Write(c, p)
}

func (a api) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.FullPath(), "/api/") {
			c.Next()
			return
		}
		if a.deps.Backend == nil || a.deps.Verifier == nil {
			problemhttp.Write(c, problem.New(problem.Unavailable, "The business API is not configured."))
			return
		}
		headers := c.Request.Header.Values("Authorization")
		if len(headers) == 0 {
			problemhttp.Write(c, problem.New(problem.AuthenticationRequired, "Provide a Bearer session token."))
			return
		}
		fields := strings.Fields(headers[0])
		if len(headers) != 1 || len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 16384 {
			problemhttp.Write(c, problem.New(problem.InvalidToken, "Provide exactly one Bearer session token."))
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		who, err := a.deps.Verifier.Verify(ctx, fields[1])
		if err != nil {
			respondError(c, err)
			return
		}
		observability.SetUser(c, "clerk:"+who.Subject, "")
		c.Set("identity", who)
		c.Next()
	}
}

func (a api) authorize() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.FullPath(), "/api/") || c.FullPath() == "/api/v1/bootstrap" {
			c.Next()
			return
		}
		actor, err := a.deps.Backend.Current(c.Request.Context(), externalIdentity(c))
		if err != nil {
			respondError(c, err)
			return
		}
		observability.SetUser(c, actor.User.ID.String(), actor.Tenant.ID.String())
		c.Set("actor", actor)
		c.Next()
	}
}

func externalIdentity(c *gin.Context) auth.Identity {
	v, _ := c.Get("identity")
	who, ok := v.(auth.Identity)
	if !ok {
		panic("missing verified identity")
	}
	return who
}

func localActor(c *gin.Context) identity.Context {
	v, _ := c.Get("actor")
	actor, ok := v.(identity.Context)
	if !ok {
		panic("missing authorized actor")
	}
	return actor
}

// decode rejects unknown fields and trailing JSON; empty bodies are only valid for bootstrap retries.
func decode(c *gin.Context, out any, emptyOK bool) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		problemhttp.Write(c, problem.New(problem.InvalidRequest, "The request body must not exceed 16384 bytes."))
		return false
	}
	if len(strings.TrimSpace(string(data))) == 0 && emptyOK {
		return true
	}
	trimmed := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trimmed, "{") {
		problemhttp.Write(c, problem.New(problem.InvalidRequest, "Provide a JSON object."))
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(out); err != nil {
		problemhttp.Write(c, problem.New(problem.InvalidRequest, "Provide a JSON object with supported fields and value types."))
		return false
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		problemhttp.Write(c, problem.New(problem.InvalidRequest, "Provide exactly one JSON object."))
		return false
	}
	return true
}

func (a api) Bootstrap(c *gin.Context, _ apiv1.BootstrapParams) {
	var in apiv1.BootstrapInput
	if !decode(c, &in, true) {
		return
	}
	result, created, err := a.deps.Backend.Bootstrap(c.Request.Context(), externalIdentity(c), identity.BootstrapInput{BaseCurrency: stringValue(in.BaseCurrency), Timezone: stringValue(in.Timezone), Locale: stringValue(in.Locale)})
	if err != nil {
		respondError(c, err)
		return
	}
	observability.SetUser(c, result.User.ID.String(), result.Tenant.ID.String())
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, contextDTO(result))
}

func (a api) GetMe(c *gin.Context, _ apiv1.GetMeParams) {
	c.JSON(http.StatusOK, contextDTO(localActor(c)))
}

func (a api) ListBooks(c *gin.Context, _ apiv1.ListBooksParams) {
	result, err := a.deps.Backend.Books(c.Request.Context(), localActor(c))
	if err != nil {
		respondError(c, err)
		return
	}
	books := make([]apiv1.Book, len(result))
	for i, book := range result {
		books[i] = bookDTO(book)
	}
	c.JSON(http.StatusOK, apiv1.BookList{Books: books})
}

func (a api) GetBook(c *gin.Context, id uuid.UUID, _ apiv1.GetBookParams) {
	result, err := a.deps.Backend.Book(c.Request.Context(), localActor(c), id)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, bookDTO(result))
}

func (a api) UpdatePreferences(c *gin.Context, _ apiv1.UpdatePreferencesParams) {
	var in apiv1.PreferencesPatch
	if !decode(c, &in, false) {
		return
	}
	result, err := a.deps.Backend.UpdatePreferences(c.Request.Context(), localActor(c), identity.PreferencesPatch{Locale: in.Locale, Timezone: in.Timezone, Theme: in.Theme})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, preferencesDTO(result))
}

func (a api) GetInstance(c *gin.Context, _ apiv1.GetInstanceParams) {
	result, err := a.deps.Backend.Instance(c.Request.Context(), localActor(c))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, apiv1.Instance{AdminUserId: result.AdminUserID, InitializedAt: result.InitializedAt})
}

func (a api) ListUsers(c *gin.Context, params apiv1.ListUsersParams) {
	limit := int32(50)
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 || limit > 100 {
		problemhttp.Write(c, problem.New(problem.InvalidRequest, "Use a UUID after cursor and a limit between 1 and 100."))
		return
	}

	result, err := a.deps.Backend.Users(c.Request.Context(), localActor(c), params.After, limit)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, userPageDTO(result))
}

func (a api) SetUserStatus(c *gin.Context, id uuid.UUID, _ apiv1.SetUserStatusParams) {
	var in apiv1.StatusInput
	if !decode(c, &in, false) {
		return
	}
	result, err := a.deps.Backend.SetStatus(c.Request.Context(), localActor(c), id, identity.StatusInput{Status: string(in.Status), Reason: in.Reason})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, userDTO(result))
}
