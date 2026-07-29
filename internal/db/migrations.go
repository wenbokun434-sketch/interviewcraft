package db

type migration struct {
	version int
	name    string
	sql     string
}

var defaultMigrations = []migration{
	{
		version: 1,
		name:    "initial_local_storage",
		sql:     initialSchemaSQL,
	},
}

const initialSchemaSQL = `
CREATE TABLE candidate_profiles (
    id TEXT PRIMARY KEY,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    confirmed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE scenarios (
    id TEXT PRIMARY KEY,
    profile_id TEXT NOT NULL REFERENCES candidate_profiles(id) ON DELETE CASCADE,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    prompt_version TEXT NOT NULL,
    confirmed_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX scenarios_profile_id_idx ON scenarios(profile_id);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    scenario_id TEXT NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX sessions_scenario_id_idx ON sessions(scenario_id);

CREATE TABLE session_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    speaker TEXT NOT NULL,
    question_id TEXT NOT NULL,
    content TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    evidence_refs_json TEXT NOT NULL CHECK (json_valid(evidence_refs_json))
);
CREATE INDEX session_events_order_idx
    ON session_events(session_id, occurred_at, sequence);

CREATE TABLE drafts (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    question_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    content TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (session_id, question_id, kind)
);

CREATE TABLE sidebar_events (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    question_id TEXT NOT NULL,
    intent TEXT NOT NULL,
    help_level TEXT NOT NULL,
    tags_json TEXT NOT NULL CHECK (json_valid(tags_json)),
    outcome TEXT NOT NULL,
    paused_timer INTEGER NOT NULL CHECK (paused_timer IN (0, 1)),
    occurred_at TEXT NOT NULL
);
CREATE INDEX sidebar_events_session_idx
    ON sidebar_events(session_id, occurred_at, id);

CREATE TABLE code_submissions (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    question_id TEXT NOT NULL,
    language TEXT NOT NULL,
    source TEXT NOT NULL,
    test_result_json TEXT NOT NULL CHECK (json_valid(test_result_json)),
    runtime_stats_json TEXT NOT NULL CHECK (json_valid(runtime_stats_json)),
    snapshot_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX code_submissions_session_idx
    ON code_submissions(session_id, created_at, id);

CREATE TABLE reports (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE CASCADE,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE provider_configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    secret_ref TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    updated_at TEXT NOT NULL
);
`
