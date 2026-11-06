-- +goose Up

-- Agent and workflow versions are immutable: every change is a new version,
-- and runs and messages point at the exact version they used. Only updates
-- are blocked: deleting a whole agent or workflow still cascades to its
-- versions (runs referencing them stop that while any exist).
-- +goose StatementBegin
create function forbid_version_change() returns trigger language plpgsql as $$
begin
    raise exception '% rows are immutable; add a new version instead', tg_table_name;
end;
$$;
-- +goose StatementEnd

create trigger agent_versions_immutable before update on agent_versions
    for each row execute function forbid_version_change();

create trigger workflow_versions_immutable before update on workflow_versions
    for each row execute function forbid_version_change();

-- +goose Down
drop trigger workflow_versions_immutable on workflow_versions;
drop trigger agent_versions_immutable on agent_versions;
drop function forbid_version_change();
