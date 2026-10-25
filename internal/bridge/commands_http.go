package bridge

import "net/http"

func (a *App) commandRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("POST /api/commands", control(func(w http.ResponseWriter, r *http.Request) error {
		if !a.config.Bridge.AllowWebCommandEditing {
			return fail(403, "command_editing_disabled", "Command editing is disabled in config.yaml. Change the file and restart WebDeviceCable.")
		}
		var v Action
		if e := decode(w, r, &v); e != nil {
			return e
		}
		if e := validateAction(&v); e != nil {
			return fail(400, "invalid_command", e.Error())
		}
		if _, exists := a.actions[v.ID]; exists {
			return fail(409, "duplicate_command", "Choose a unique command id.")
		}
		if e := a.editAction(v.ID, &v); e != nil {
			return e
		}
		writeJSON(w, 201, a.actions[v.ID])
		return nil
	}))
	mux.HandleFunc("PUT /api/commands/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		if !a.config.Bridge.AllowWebCommandEditing {
			return fail(403, "command_editing_disabled", "Command editing is disabled in config.yaml. Change the file and restart WebDeviceCable.")
		}
		var v Action
		if e := decode(w, r, &v); e != nil {
			return e
		}
		id := r.PathValue("id")
		if _, ok := a.actions[id]; !ok {
			return fail(404, "not_found", "Command not found.")
		}
		if e := a.editAction(id, &v); e != nil {
			return e
		}
		writeJSON(w, 200, a.actions[id])
		return nil
	}))
	mux.HandleFunc("DELETE /api/commands/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		if e := a.editAction(r.PathValue("id"), nil); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
}
