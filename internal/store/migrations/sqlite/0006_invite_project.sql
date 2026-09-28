-- project, access: an invitation that also makes its invitee a member of that project (participant | reader).
ALTER TABLE invites ADD COLUMN project TEXT NOT NULL DEFAULT '';
ALTER TABLE invites ADD COLUMN access TEXT NOT NULL DEFAULT '';
