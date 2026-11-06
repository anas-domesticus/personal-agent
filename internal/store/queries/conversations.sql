-- name: CreateConversation :one
insert into conversations (agent_id, title)
values ($1, $2)
returning *;

-- name: GetConversation :one
select c.*, a.name as agent_name
from conversations c
join agents a on a.id = c.agent_id
where c.id = $1;

-- name: ListConversations :many
select c.*, a.name as agent_name
from conversations c
join agents a on a.id = c.agent_id
order by c.updated_at desc
limit $1;

-- name: SetConversationTitle :exec
update conversations set title = $2 where id = $1;

-- name: TouchConversation :exec
update conversations set updated_at = now() where id = $1;

-- name: AddMessage :one
insert into messages (conversation_id, role, content, message, agent_version)
values ($1, $2, $3, $4, $5)
returning id;

-- name: ListMessages :many
select * from messages
where conversation_id = $1
order by id;
