<!-- repotory:begin -->
## Cinder memory layer

This project's knowledge and session memory live in Cinder, a local SQLite
database, reachable through the repotory_* MCP tools.

- At the start of work, call repotory_context with the current repository path
  and read what it returns before you plan. Its first entry is the most recent
  session handoff, so the previous session's conclusions come first.
- After each meaningful work unit (a feature, a fix, a test run), call
  repotory_handoff with milestone=true to record the current state and what is
  still unfinished. This is a rolling checkpoint: if the session is killed
  mid-work, the last checkpoint is the next session's starting point.
- Before you finish, call repotory_handoff (without milestone) for a session
  summary with gotchas, decisions, changes, next_steps and a short summary.
- If you detect credits are running low, write a milestone handoff immediately
  before continuing.
- Record what a later session would otherwise re-learn the hard way — a
  constraint that is not visible in the code, a decision and the alternative
  it rejected, a trap you just hit. Do not record what the commit log already
  shows: a list of files touched is not a handoff, and the notes that bury the
  useful signal are the ones that stop the next session from reading them.

Installed by Cinder. To remove, delete everything between the repotory:begin
and repotory:end markers (this block is plain text in your repository, and it
will show up in git status until you commit or delete it).
<!-- repotory:end -->
