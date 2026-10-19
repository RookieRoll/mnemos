-- 0008_session_host_id.sql — map mnemos sessions to the host's own session.
--
-- Hooks used to find "their" session as the newest open one across every
-- project. With parallel terminals that picked another project's session
-- most of the time (2026-07-27 to 2026-09-30: 53% of prompt_hook, 72% of
-- pre_tool and 90% of recovery injections landed on the wrong project's
-- session). Claude Code sends its own session_id in every hook payload, so
-- the session it opens at SessionStart can be found again exactly.
--
-- Nullable: sessions opened by agents calling mnemos_session_start, by
-- hosts that send no session id, and every session before this migration
-- carry none.

-- Version six diverged before the branches merged: upstream used it for
-- host_session_id, while the local branch used it for observations.pinned.
-- The version-eight migration runner adds whichever column is absent in this
-- same transaction. Unconditional ALTER statements would break one lineage.
CREATE INDEX IF NOT EXISTS idx_obs_pinned ON observations(pinned);
CREATE INDEX IF NOT EXISTS idx_sessions_host ON sessions(host_session_id) WHERE host_session_id IS NOT NULL;
