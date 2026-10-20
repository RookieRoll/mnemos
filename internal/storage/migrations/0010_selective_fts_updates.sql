-- Metadata bookkeeping must not retokenize unchanged external-content rows.
-- Keep insert/delete maintenance and the historical migration SQL unchanged.
DROP TRIGGER observations_au;
CREATE TRIGGER observations_au
AFTER UPDATE OF title, content, tags ON observations
WHEN old.title IS NOT new.title OR old.content IS NOT new.content OR old.tags IS NOT new.tags
BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, title, content, tags)
    VALUES ('delete', old.rowid, old.title, old.content, old.tags);
    INSERT INTO observations_fts(rowid, title, content, tags)
    VALUES (new.rowid, new.title, new.content, new.tags);
END;

DROP TRIGGER skills_au;
CREATE TRIGGER skills_au
AFTER UPDATE OF name, description, procedure, tags ON skills
WHEN old.name IS NOT new.name OR old.description IS NOT new.description
    OR old.procedure IS NOT new.procedure OR old.tags IS NOT new.tags
BEGIN
    INSERT INTO skills_fts(skills_fts, rowid, name, description, procedure, tags)
    VALUES ('delete', old.rowid, old.name, old.description, old.procedure, old.tags);
    INSERT INTO skills_fts(rowid, name, description, procedure, tags)
    VALUES (new.rowid, new.name, new.description, new.procedure, new.tags);
END;
