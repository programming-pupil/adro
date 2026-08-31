-- Bind provider provenance to the durable dispatch key. This lets recovery
-- acknowledge a side effect that committed before the process lost its reply.
alter table provenance add column if not exists provider_idempotency_key text;
create index if not exists provenance_work_item_idempotency
  on provenance(work_item_id, provider_idempotency_key);
