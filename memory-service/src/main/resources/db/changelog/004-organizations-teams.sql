-- Organizations
CREATE TABLE IF NOT EXISTS organizations (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,
    metadata    JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_organizations_slug
    ON organizations (slug) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_organizations_not_deleted
    ON organizations (deleted_at) WHERE deleted_at IS NULL;

-- Organization members
CREATE TABLE IF NOT EXISTS organization_members (
    organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    role            TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (organization_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_organization_members_user
    ON organization_members (user_id);

-- Teams
CREATE TABLE IF NOT EXISTS teams (
    id              UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    CONSTRAINT unique_team_slug_per_org UNIQUE (organization_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_teams_organization
    ON teams (organization_id) WHERE deleted_at IS NULL;

-- Team members
CREATE TABLE IF NOT EXISTS team_members (
    team_id     UUID NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (team_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_team_members_user
    ON team_members (user_id);

-- Add organization and team references to conversation_groups
ALTER TABLE conversation_groups
    ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations (id) ON DELETE SET NULL;

ALTER TABLE conversation_groups
    ADD COLUMN IF NOT EXISTS team_id UUID REFERENCES teams (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_conversation_groups_organization
    ON conversation_groups (organization_id) WHERE organization_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_conversation_groups_team
    ON conversation_groups (team_id) WHERE team_id IS NOT NULL;
