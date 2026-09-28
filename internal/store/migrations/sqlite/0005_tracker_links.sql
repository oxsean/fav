-- parent: the issue whose task's subtask this sub-issue mirrors (0: a requirement); pr: the pull or merge request opened from its task's branch.
ALTER TABLE tracker_issues ADD COLUMN parent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracker_issues ADD COLUMN pr TEXT NOT NULL DEFAULT '';
