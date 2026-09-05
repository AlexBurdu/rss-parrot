CREATE TABLE toot_summaries
(
    account_id     INTEGER  NOT NULL,
    status_id      TEXT     NOT NULL,
    article_text   TEXT     NOT NULL DEFAULT (''),
    attempts       INTEGER  NOT NULL DEFAULT 0,
    next_retry_due DATETIME NOT NULL,
    state          INTEGER  NOT NULL DEFAULT 0,
    FOREIGN KEY (account_id) REFERENCES accounts (id)
);
CREATE UNIQUE INDEX idx_150 ON toot_summaries (status_id);
CREATE INDEX idx_151 ON toot_summaries (state, next_retry_due);
