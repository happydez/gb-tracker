-- Mods this service has finished processing.
-- Events are delivered at least once, so a mod arriving twice must not be
-- downloaded, uploaded and announced again.
CREATE TABLE IF NOT EXISTS mods (
    mod_id INTEGER PRIMARY KEY,

    name TEXT NOT NULL DEFAULT '',
    mdate INTEGER NOT NULL DEFAULT 0,

    -- skipped means nothing matched the prefixes; it is still recorded so the
    -- mod is not fetched again on redelivery.
    status TEXT NOT NULL DEFAULT 'done',

    map_count INTEGER NOT NULL DEFAULT 0,
    bsp_size INTEGER NOT NULL DEFAULT 0,
    bz2_size INTEGER NOT NULL DEFAULT 0,

    uploaded INTEGER NOT NULL DEFAULT 0,
    rcon_done INTEGER NOT NULL DEFAULT 0,

    last_error TEXT NOT NULL DEFAULT '',

    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
) STRICT;

-- GameBanana file ids already processed. A mod edited without touching its
-- files gets a new mdate but the same files, and there is nothing to redo.
CREATE TABLE IF NOT EXISTS files (
    file_id TEXT PRIMARY KEY,
    mod_id INTEGER NOT NULL,
    md5 TEXT NOT NULL DEFAULT '',
    processed_at INTEGER NOT NULL DEFAULT 0
) STRICT;

-- Maps uploaded to FastDL, so RCON knows what exists and re-uploads are cheap
-- to detect.
CREATE TABLE IF NOT EXISTS maps (
    name TEXT NOT NULL,
    mod_id INTEGER NOT NULL,
    remote_path TEXT NOT NULL DEFAULT '',
    bz2_size INTEGER NOT NULL DEFAULT 0,
    uploaded_at INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (name, mod_id)
) STRICT;
