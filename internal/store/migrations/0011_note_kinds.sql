-- Work records: notes yip writes itself when work completes (the outcome,
-- final revision, approvals, checks, and how review suggestions were
-- handled), so an engineer remembers finished work without curation.
ALTER TABLE engineer_notes ADD COLUMN kind TEXT NOT NULL DEFAULT 'note';
CREATE INDEX engineer_notes_record ON engineer_notes(engineer_id, kind);
