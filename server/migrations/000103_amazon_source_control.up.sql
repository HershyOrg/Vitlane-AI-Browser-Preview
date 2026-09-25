-- The environment is a deployment capability; this switch controls runtime admission.
CREATE TABLE research_catalog_source_control (
 source TEXT PRIMARY KEY CHECK (source='AMAZON'),
 enabled BOOLEAN NOT NULL DEFAULT true,
 version BIGINT NOT NULL DEFAULT 1 CHECK (version>0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO research_catalog_source_control(source) VALUES ('AMAZON');
CREATE TABLE research_catalog_source_control_audit (
 source TEXT NOT NULL REFERENCES research_catalog_source_control(source),
 version BIGINT NOT NULL,
 enabled BOOLEAN NOT NULL,
 operator_user_id UUID NOT NULL REFERENCES users(id),
 changed_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(source,version)
);
