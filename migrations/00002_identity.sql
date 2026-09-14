-- +goose Up
CREATE TABLE users (
 id uuid PRIMARY KEY,
 display_name text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE user_identities (
 issuer text NOT NULL,
 subject text NOT NULL,
 user_id uuid NOT NULL REFERENCES users(id),
 PRIMARY KEY (issuer, subject)
);
CREATE TABLE tenants (
 id uuid PRIMARY KEY,
 name text NOT NULL,
 kind text NOT NULL DEFAULT 'personal' CHECK (kind = 'personal'),
 status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE tenant_members (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 user_id uuid NOT NULL REFERENCES users(id),
 role text NOT NULL DEFAULT 'owner' CHECK (role = 'owner'),
 status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
 PRIMARY KEY (tenant_id, user_id)
);
CREATE TABLE books (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 name text NOT NULL,
 base_currency text NOT NULL CHECK (base_currency ~ '^[A-Z]{3}$'),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (tenant_id, id)
);
CREATE TABLE personal_tenant_bindings (
 user_id uuid PRIMARY KEY REFERENCES users(id),
 tenant_id uuid NOT NULL UNIQUE REFERENCES tenants(id),
 default_book_id uuid NOT NULL,
 FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_members(tenant_id, user_id),
 FOREIGN KEY (tenant_id, default_book_id) REFERENCES books(tenant_id, id)
);
CREATE TABLE user_preferences (
 user_id uuid PRIMARY KEY REFERENCES users(id),
 locale text NOT NULL,
 timezone text NOT NULL,
 theme text NOT NULL DEFAULT 'system' CHECK (theme IN ('system', 'light', 'dark'))
);
CREATE TABLE instance_state (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 admin_user_id uuid REFERENCES users(id),
 initialized_at timestamptz,
 CHECK ((admin_user_id IS NULL) = (initialized_at IS NULL))
);
INSERT INTO instance_state (singleton) VALUES (true);
CREATE TABLE admin_audit_events (
 id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 target_user_id uuid NOT NULL REFERENCES users(id),
 previous_status text NOT NULL CHECK (previous_status IN ('active', 'disabled')),
 new_status text NOT NULL CHECK (new_status IN ('active', 'disabled')),
 reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
 created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose Down
DROP TABLE admin_audit_events;
DROP TABLE instance_state;
DROP TABLE user_preferences;
DROP TABLE personal_tenant_bindings;
DROP TABLE books;
DROP TABLE tenant_members;
DROP TABLE tenants;
DROP TABLE user_identities;
DROP TABLE users;
