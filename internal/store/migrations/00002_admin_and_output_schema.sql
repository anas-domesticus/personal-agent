-- +goose Up

-- Admin agents get the native tools for managing agents and workflows
-- injected at build time. Deliberately on agents, not agent_versions, and
-- not settable through any tool: only a human can make an agent an admin.
alter table agents add column admin boolean not null default false;

-- JSON Schema for the structured object this agent outputs. Null means the
-- agent's output is wrapped as {"text": "..."}.
alter table agent_versions add column output_schema jsonb
    check (output_schema is null or jsonb_typeof(output_schema) = 'object');

-- +goose Down
alter table agent_versions drop column output_schema;
alter table agents drop column admin;
