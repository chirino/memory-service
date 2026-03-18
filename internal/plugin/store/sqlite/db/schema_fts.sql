CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
    indexed_content,
    content='entries',
    content_rowid='rowid',
    tokenize='unicode61'
);

CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
  INSERT INTO entries_fts(rowid, indexed_content) VALUES (new.rowid, COALESCE(new.indexed_content, ''));
END;

CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, indexed_content) VALUES ('delete', old.rowid, COALESCE(old.indexed_content, ''));
END;

CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, indexed_content) VALUES ('delete', old.rowid, COALESCE(old.indexed_content, ''));
  INSERT INTO entries_fts(rowid, indexed_content) VALUES (new.rowid, COALESCE(new.indexed_content, ''));
END;
