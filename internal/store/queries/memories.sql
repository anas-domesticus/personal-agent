-- name: CreateMemory :one
insert into memories (content, tags, source, metadata, embedding, embedding_model)
values (sqlc.arg(content), sqlc.arg(tags)::text[], sqlc.arg(source), sqlc.arg(metadata),
        sqlc.narg(embedding), sqlc.narg(embedding_model))
returning id, content, tags, source, metadata, embedding_model, created_at, updated_at;

-- name: GetMemory :one
select id, content, tags, source, metadata, embedding_model, created_at, updated_at
from memories
where id = $1;

-- name: SetMemoryEmbedding :execrows
-- Only writes if the content is still what was embedded; returns 0 rows otherwise.
update memories
set embedding = sqlc.arg(embedding), embedding_model = sqlc.arg(embedding_model)
where id = sqlc.arg(id) and content = sqlc.arg(content);

-- name: ListMemoriesMissingEmbedding :many
select id
from memories
where embedding is null or embedding_model is distinct from sqlc.arg(model)::text
limit sqlc.arg(lim);

-- name: SearchMemories :many
-- Hybrid search: reciprocal rank fusion of vector similarity and full-text rank.
-- With use_semantic false, only the keyword side contributes. Only vectors
-- from the query's embedding model are compared.
-- An empty tags array matches everything; otherwise memories must have all given tags.
with semantic as (
    select id, row_number() over (order by distance) as rank
    from (
        select id, embedding <=> sqlc.arg(embedding)::vector as distance
        from memories
        where sqlc.arg(use_semantic)::bool and embedding is not null
          and embedding_model = sqlc.arg(model)::text
          and (cardinality(sqlc.arg(tags)::text[]) = 0 or tags @> sqlc.arg(tags)::text[])
        order by embedding <=> sqlc.arg(embedding)::vector
        limit sqlc.arg(candidates)::int
    ) s
),
keyword as (
    select id, row_number() over (order by ts_rank_cd(search, q) desc) as rank
    from memories, websearch_to_tsquery('english', sqlc.arg(query)::text) q
    where search @@ q
      and (cardinality(sqlc.arg(tags)::text[]) = 0 or tags @> sqlc.arg(tags)::text[])
    limit sqlc.arg(candidates)::int
)
select m.id, m.content, m.tags, m.source, m.metadata, m.created_at,
       (coalesce(1.0 / (60 + s.rank), 0) + coalesce(1.0 / (60 + k.rank), 0))::float8 as score
from semantic s
full outer join keyword k on k.id = s.id
join memories m on m.id = coalesce(s.id, k.id)
order by score desc
limit sqlc.arg(lim)::int;
