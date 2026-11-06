-- +goose Up

-- Tools move into the version's config as an object: {"name": true | false |
-- {settings}}. Existing lists become {"name": true, ...}. This is the one
-- sanctioned rewrite of version rows, so the immutability trigger is paused.
alter table agent_versions disable trigger agent_versions_immutable;

update agent_versions
set config = config || jsonb_build_object('tools',
    coalesce((select jsonb_object_agg(t, true) from unnest(tools) as t), '{}'::jsonb));

alter table agent_versions drop column tools;

alter table agent_versions enable trigger agent_versions_immutable;

-- Subagent conversations. A subagent is either a stored agent (agent_id set)
-- or an ad-hoc worker (agent_id null); `subagent` records how it was built
-- so the conversation can be continued. parent_id links it to the
-- conversation that started it.
alter table conversations alter column agent_id drop not null;
alter table conversations add column parent_id uuid references conversations(id) on delete cascade;
alter table conversations add column subagent jsonb check (subagent is null or jsonb_typeof(subagent) = 'object');
alter table conversations add constraint conversations_agent_or_subagent
    check (agent_id is not null or subagent is not null);

create index conversations_parent_idx on conversations (parent_id) where parent_id is not null;

-- +goose Down
drop index conversations_parent_idx;
alter table conversations drop constraint conversations_agent_or_subagent;
alter table conversations drop column subagent;
alter table conversations drop column parent_id;
delete from conversations where agent_id is null;
alter table conversations alter column agent_id set not null;

alter table agent_versions disable trigger agent_versions_immutable;
alter table agent_versions add column tools text[] not null default '{}';
update agent_versions
set tools = coalesce(array(select k from jsonb_each(config->'tools') as e(k, v) where v <> 'false'::jsonb), '{}'),
    config = config - 'tools';
alter table agent_versions enable trigger agent_versions_immutable;
