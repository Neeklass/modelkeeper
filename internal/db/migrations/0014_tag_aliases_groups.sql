-- 0014: tag aliases and tag groups (G1). An alias is another spelling of a
-- tag ("elves" -> "elf"): adding the alias adds the tag. A group sorts tags
-- in the sidebar ("Race": elf, orc; "Scale": 28mm, 75mm). Both are keyed by
-- the tag's name, not its id: a tag that is no longer used disappears, but
-- its aliases and group stay for the next time it is used.
CREATE TABLE tag_alias (
    alias      TEXT    PRIMARY KEY COLLATE NOCASE,
    target     TEXT    NOT NULL COLLATE NOCASE,
    created_at INTEGER NOT NULL
);
CREATE INDEX tag_alias_target ON tag_alias (target);

CREATE TABLE tag_group (
    tag TEXT PRIMARY KEY COLLATE NOCASE,
    grp TEXT NOT NULL
);
