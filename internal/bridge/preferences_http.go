package bridge

import (
	"encoding/json"
	"net/http"
)

func (a *App) preferenceRoutes(mux *http.ServeMux, paired func(func(http.ResponseWriter, *http.Request, *Pairing) error) http.HandlerFunc, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("POST /v2/groups", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		var in struct {
			Groups []ActionGroup `json:"groups"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := validateGroups(in.Groups); err != nil {
			return fail(400, "invalid_groups", err.Error())
		}
		groups := append([]ActionGroup{}, p.Groups...)
		for _, incoming := range in.Groups {
			found := false
			for i, old := range groups {
				if old.ID == incoming.ID {
					groups[i] = incoming
					found = true
					break
				}
			}
			if !found {
				groups = append(groups, incoming)
			}
		}
		if err := validateGroups(groups); err != nil {
			return fail(400, "group_limit", err.Error())
		}
		if err := validateGroupTree(groups); err != nil {
			return fail(400, "invalid_groups", err.Error())
		}
		// Materialize only pre-existing local choices, once per new group. The
		// website supplies aliases, never a program/model or new permission.
		routing := p.Routing
		migrated := false
		routing.Groups = map[string]ExecutionTarget{}
		for id, target := range p.Routing.Groups {
			routing.Groups[id] = target
		}
		for _, incoming := range in.Groups {
			if groupExists(p, incoming.ID) {
				continue
			}
			for _, legacy := range incoming.Replaces {
				custom := false
				for id, depth := legacy, 0; id != "" && depth < 8; depth++ {
					if _, ok := p.Routing.Groups[id]; ok {
						custom = true
						break
					}
					parent := ""
					for _, old := range p.Groups {
						if old.ID == id {
							parent = old.ParentID
							break
						}
					}
					id = parent
				}
				if custom {
					routing.Groups[incoming.ID] = routingTarget(p, legacy)
					migrated = true
					break
				}
			}
		}
		if migrated {
			routing.Revision++
		}
		hierarchyChanged := false
		for _, incoming := range in.Groups {
			for _, old := range p.Groups {
				if incoming.ID == old.ID && (incoming.ParentID != old.ParentID || incoming.Kind != old.Kind) {
					hierarchyChanged = true
				}
			}
		}
		if err := a.update(func(s *State) error {
			next := s.Pairings[p.ID]
			next.Groups = groups
			next.GroupsDeclared = true
			next.Routing = routing
			if hierarchyChanged {
				next.Routing.Revision++
				for _, conversation := range s.Conversations {
					if conversation.PairingID == p.ID && conversation.Group != "" && conversation.Status == "open" {
						conversation.Status = "closed"
						conversation.Code = "groups_changed"
						conversation.UpdatedAt = a.now()
					}
				}
				for _, job := range s.Jobs {
					if job.PairingID == p.ID && job.Request.Group != "" && !terminal(job.Status) {
						finish(job, "cancelled", "groups_changed", "The website changed its group hierarchy. Submit a new request.", a.now())
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"groups": groups})
		return nil
	}))
	mux.HandleFunc("GET /v2/groups", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		groups := p.Groups
		if groups == nil {
			groups = []ActionGroup{}
		}
		writeJSON(w, 200, map[string]any{"groups": groups})
		return nil
	}))
	mux.HandleFunc("PUT /api/pairings/{id}/routing", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			ExpectedRevision uint64  `json:"expectedRevision"`
			Routing          Routing `json:"routing"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		p := a.state.Pairings[r.PathValue("id")]
		if p == nil || p.Revoked {
			return fail(404, "not_found", "Connection not found.")
		}
		if p.Routing.Revision != in.ExpectedRevision {
			return fail(409, "preferences_changed", "These settings changed in another panel. Reopen Routing and review them.")
		}
		if in.Routing.Default != nil {
			if err := a.validateTarget(p, *in.Routing.Default); err != nil {
				return err
			}
		}
		if len(in.Routing.Groups) > 64 {
			return fail(400, "invalid_route", "Too many group overrides.")
		}
		for id, target := range in.Routing.Groups {
			if !groupExists(p, id) {
				return fail(400, "unknown_group", "Only the website can declare action groups.")
			}
			for _, group := range p.Groups {
				if group.ID == id && group.Kind != "ai" && target.Provider != "" {
					return fail(400, "invalid_route", "Ordinary groups can only use commands.")
				}
			}
			if target.ModelDefault && target.Model != "" || target.ReasoningDefault && target.Reasoning != "" || target.Action != "" && (target.InheritOptions || target.ModelDefault || target.ReasoningDefault) {
				return fail(400, "invalid_route", "Choose either inheritance, client default or an explicit native option.")
			}
			preview := *p
			preview.Routing = in.Routing
			if err := a.validateTarget(p, routingTarget(&preview, id)); err != nil {
				return err
			}
		}
		in.Routing.Revision = p.Routing.Revision + 1
		if err := a.update(func(s *State) error {
			next := s.Pairings[p.ID]
			next.Routing = in.Routing
			a.closeConversations(s, p.ID, "route_changed", a.now())
			for _, job := range s.Jobs {
				if job.PairingID == p.ID && !terminal(job.Status) && (job.Request.Kind == "action" || job.Request.Group != "") {
					finish(job, "cancelled", "route_changed", "Routing changed. Submit a new request after reviewing the selection.", a.now())
				}
			}
			return nil
		}); err != nil {
			return err
		}
		writeJSON(w, 200, a.state.Pairings[p.ID].Routing)
		return nil
	}))
	mux.HandleFunc("PATCH /api/history", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Keep           *bool `json:"keep"`
			ConfirmDiscard bool  `json:"confirmDiscard"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Keep == nil {
			return fail(400, "invalid_history", "Choose whether to keep request history.")
		}
		if globalRequestHistory(&a.state) && !*in.Keep && !in.ConfirmDiscard {
			return fail(400, "history_confirmation_required", "Confirm removal of finished requests that use the global history setting.")
		}
		if err := a.update(func(s *State) error {
			changed := globalRequestHistory(s) != *in.Keep
			s.KeepRequestHistory = in.Keep
			if changed && !*in.Keep {
				a.discardStoredHistory(s, func(j *Job) bool { return !keepRequestHistory(s, s.Pairings[j.PairingID]) })
			}
			return nil
		}); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"keepRequestHistory": globalRequestHistory(&a.state)})
		return nil
	}))
	mux.HandleFunc("PATCH /api/sharing", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Names     *bool `json:"names"`
			Versions  *bool `json:"versions"`
			Models    *bool `json:"models"`
			RawErrors *bool `json:"rawErrors"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if err := a.update(func(s *State) error {
			before, _ := json.Marshal(s.Sharing)
			if in.Names != nil {
				s.Sharing.Names = *in.Names
			}
			if in.Versions != nil {
				s.Sharing.Versions = *in.Versions
			}
			if in.Models != nil {
				s.Sharing.Models = *in.Models
			}
			if in.RawErrors != nil {
				s.Sharing.RawErrors = *in.RawErrors
			}
			after, _ := json.Marshal(s.Sharing)
			if string(before) != string(after) {
				for _, job := range s.Jobs {
					job.Revision++
				}
			}
			return nil
		}); err != nil {
			return err
		}
		writeJSON(w, 200, a.state.Sharing)
		return nil
	}))
}
