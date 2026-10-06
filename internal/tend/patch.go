package tend

import (
	"strings"
	"time"
)

// Patch sets fields of a record: each non-nil field is the value it gets, so applying a patch twice changes nothing more.
type Patch struct {
	Favorite *bool     `json:"favorite,omitempty"`
	Archived *bool     `json:"archived,omitempty"`
	Status   *string   `json:"status,omitempty"` // todo | doing | done
	Title    *string   `json:"title,omitempty"`
	Tags     *[]string `json:"tags,omitempty"` // Normalize'd by Apply
	Summary  *string   `json:"summary,omitempty"`
	Label    *string   `json:"label,omitempty"`
	Project  *string   `json:"project,omitempty"`
	WorkType *string   `json:"work_type,omitempty"`
	// FavoritedAt and ArchivedAt, with Favorite or Archived true: the time to record (Undo puts the old one back); nil
	// keeps a time already set, else now.
	FavoritedAt *time.Time `json:"favorited_at,omitempty"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
}

// Empty: p sets nothing.
func (p Patch) Empty() bool {
	return p.Favorite == nil && p.Archived == nil && p.Status == nil && p.Title == nil && p.Tags == nil &&
		p.Summary == nil && p.Label == nil && p.Project == nil && p.WorkType == nil
}

// Apply sets p's fields on r; a favorite or archive set true and unset before gets now (or p's time).
func (p Patch) Apply(r *Rec, now time.Time) {
	if p.Favorite != nil {
		r.FavoritedAt = since(*p.Favorite, r.FavoritedAt, p.FavoritedAt, now)
	}
	if p.Archived != nil {
		r.ArchivedAt = since(*p.Archived, r.ArchivedAt, p.ArchivedAt, now)
	}
	if p.Status != nil {
		r.Status = *p.Status
	}
	if p.Title != nil {
		r.Title = strings.TrimSpace(*p.Title)
	}
	if p.Tags != nil {
		r.Tags = Normalize(*p.Tags)
	}
	if p.Summary != nil {
		r.Summary = strings.TrimSpace(*p.Summary)
	}
	if p.Label != nil {
		r.Label = strings.TrimSpace(*p.Label)
	}
	if p.Project != nil {
		r.Project = *p.Project
	}
	if p.WorkType != nil {
		r.WorkType = *p.WorkType
	}
}

func since(on bool, cur, given *time.Time, now time.Time) *time.Time {
	switch {
	case !on:
		return nil
	case given != nil:
		return new(*given)
	case cur != nil:
		return cur
	}
	return new(now)
}

// Undo is the patch that puts before's values back in the fields p sets.
func (p Patch) Undo(before *Rec) Patch {
	var u Patch
	if p.Favorite != nil {
		u.Favorite, u.FavoritedAt = new(before.Favorite()), copyTime(before.FavoritedAt)
	}
	if p.Archived != nil {
		u.Archived, u.ArchivedAt = new(before.Archived()), copyTime(before.ArchivedAt)
	}
	if p.Status != nil {
		u.Status = new(before.Status)
	}
	if p.Title != nil {
		u.Title = new(before.Title)
	}
	if p.Tags != nil {
		u.Tags = new(append([]string{}, before.Tags...)) // ⚠️ never nil: a nil list goes over JSON as null, which unsets it
	}
	if p.Summary != nil {
		u.Summary = new(before.Summary)
	}
	if p.Label != nil {
		u.Label = new(before.Label)
	}
	if p.Project != nil {
		u.Project = new(before.Project)
	}
	if p.WorkType != nil {
		u.WorkType = new(before.WorkType)
	}
	return u
}

func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	return new(*t)
}

// ToggleFavorite, ToggleArchived and ToggleStatus are the patches that turn r the other way: a status already at
// target goes back to doing.
func (r *Rec) ToggleFavorite() Patch { return Patch{Favorite: new(!r.Favorite())} }

func (r *Rec) ToggleArchived() Patch { return Patch{Archived: new(!r.Archived())} }

func (r *Rec) ToggleStatus(target string) Patch {
	if r.Status == target {
		return Patch{Status: new(StatusDoing)}
	}
	return Patch{Status: new(target)}
}
