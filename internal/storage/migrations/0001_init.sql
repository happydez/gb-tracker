-- Mods the tracker has announced, one row per GameBanana mod id.
--
-- The row is written after the event is published, so its presence means the
-- mod is done. A mod that failed anywhere has no row and the next poll retries
-- it. Crashing between publishing and inserting announces it twice, which is
-- the deliberate trade: a duplicate is survivable, a dropped mod is not.
CREATE TABLE IF NOT EXISTS mods (
    mod_id INTEGER PRIMARY KEY,

    -- Whichever watched category saw the mod first. Watching a parent and its
    -- child means both listings carry it; the primary key settles who wins.
    category_id INTEGER NOT NULL,

    -- Denormalised from record_json, so logs and ad-hoc queries stay readable.
    name TEXT NOT NULL DEFAULT '',
    profile_url TEXT NOT NULL DEFAULT '',

    date_added INTEGER NOT NULL DEFAULT 0,

    -- _tsDateModified: a larger mdate than the stored one is an update, and
    -- the mod is announced again.
    mdate INTEGER NOT NULL DEFAULT 0,

    -- ULID of the event, to trace a downstream message back to its mod.
    event_id TEXT NOT NULL DEFAULT '',

    -- Raw responses as received: a snapshot of what the event was built from.
    -- Refetching gives the mod as it is now, and nothing at all once it is
    -- deleted. detail_json is empty when details are off or failed to load --
    -- missing details never hold back an announcement.
    record_json TEXT NOT NULL DEFAULT '',
    detail_json TEXT NOT NULL DEFAULT '',

    -- First announcement, and the most recent one.
    created_at INTEGER NOT NULL DEFAULT 0,
    published_at INTEGER NOT NULL DEFAULT 0
) STRICT;
