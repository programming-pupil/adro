-- Durable transcript and deterministic memory reducer metadata.
-- PostgreSQL adapters may store transcript records independently from the
-- mutable session snapshot so replay never requires rewriting old turns.
create table if not exists session_transcript (
  session_id uuid not null,
  sequence bigint not null,
  turn_id uuid not null,
  prev_hash text,
  hash text not null,
  role text not null,
  content text not null,
  tool_call_id text,
  created_at timestamptz not null default now(),
  primary key (session_id, sequence),
  unique (session_id, turn_id),
  check (sequence > 0)
);

create index if not exists session_transcript_hash_idx on session_transcript(session_id, hash);

alter table memory_items add column if not exists fingerprint text;
create index if not exists memory_items_fingerprint_idx on memory_items(session_id, fingerprint);
