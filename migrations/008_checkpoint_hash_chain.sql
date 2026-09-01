-- Link each checkpoint to the previously committed checkpoint so recovery can
-- reject a reordered or spliced checkpoint history.
alter table session_checkpoints add column if not exists prev_hash text;
alter table session_checkpoints add column if not exists tool_call_id text;
