package bridge

// Admission is called with the app lock held. Receipts are checked before route
// resolution so retrying an old key can never execute a newly selected program.
func (a *App) createJob(p *Pairing, in JobRequest, lease string) (*Job, bool, error) {
	tools, toolHash, err := snapshotTools(in.Tools, &in.Mode)
	if err != nil {
		return nil, false, err
	}
	if in.ConversationID != "" && len(in.Tools) > 0 {
		return nil, false, fail(400, "invalid_tools", "Session messages cannot replace tools.")
	}
	in.Tools = tools
	in.ToolSetHash = toolHash
	if c := a.state.Conversations[in.ConversationID]; c != nil && c.PairingID == p.ID {
		in.ToolSetHash = c.ToolSetHash
	}
	if err := validateRequest(&in); err != nil {
		return nil, false, fail(400, "invalid_job", err.Error())
	}
	key := digest(p.ID + ":" + in.IdempotencyKey)
	hash := requestHash(in)
	receipt, consumed, err := a.receipt(key)
	if err != nil {
		return nil, false, err
	}
	if consumed {
		if c := a.state.Conversations[in.ConversationID]; in.ConversationID != "" && (c == nil || c.OmitHistory && c.Status != "open") {
			in.ToolSetHash = receipt.ToolSetHash
			hash = requestHash(in)
		}
		if receipt.ToolSetHash == "" && len(in.Tools) == 0 {
			legacy := in
			legacy.Tools = nil
			legacy.ToolSetHash = ""
			if receipt.Hash == requestHash(legacy) {
				hash = receipt.Hash
			}
		}
		if receipt.Hash != hash {
			return nil, false, fail(409, "idempotency_conflict", "This key was already used for a different request.")
		}
		job := findJob(&a.state, receipt.JobID)
		if job == nil {
			return nil, false, fail(410, "history_deleted", "This request was already consumed; its history was erased.")
		}
		if !terminal(job.Status) && job.LeaseID != lease {
			return nil, false, fail(409, "other_tab", "The original requesting tab owns this in-progress request.")
		}
		return job, true, nil
	}
	var pinned *ExecutionPlan
	if in.ConversationID != "" {
		conversation, err := a.usableConversation(p, in.ConversationID, lease)
		if err != nil {
			return nil, false, err
		}
		in.Mode = conversation.Mode
		if in.Mode == "" {
			in.Mode = "restricted"
		}
		pinned = conversation.Plan
	}
	plan, title, err := a.prepareJob(p, &in, pinned)
	if err != nil {
		return nil, false, err
	}
	if plan.Kind == "command" && a.actions[plan.Action].Session != nil && in.ConversationID == "" {
		return nil, false, fail(400, "session_required", "This command needs a session. Use the SDK session() helper.")
	}
	if (len(in.Inputs) > 0 || plan.Kind == "image-generate" || plan.Kind == "image-edit" || plan.Kind == "command" && a.actions[plan.Action].Files) && !p.Files {
		return nil, false, filePermissionDenied()
	}
	if _, err := a.contextText(p, lease, in.Contexts); err != nil {
		return nil, false, err
	}
	if len(in.Contexts) > 0 && plan.Kind == "command" && a.actions[plan.Action].Session == nil {
		return nil, false, fail(400, "invalid_context", "Text context requires a session-enabled command.")
	}
	if len(in.Inputs) > 0 {
		if _, err := a.selectedInputs(p, lease, in.Inputs); err != nil {
			return nil, false, err
		}
	}
	if plan.Kind == "command" && len(in.Inputs) > 0 && !a.actions[plan.Action].Files {
		return nil, false, fail(403, "files_not_granted", "This command has not enabled file transfers.")
	}
	imageInput := false
	for _, id := range in.Inputs {
		if input := a.inputs[id]; input != nil && (input.MediaType == "image/png" || input.MediaType == "image/jpeg" || input.MediaType == "image/webp" || input.MediaType == "image/gif") {
			imageInput = true
		}
	}
	if plan.Kind == "image-edit" && !imageInput {
		return nil, false, fail(400, "image_required", "Image editing needs a supplied PNG, JPEG, WebP or GIF.")
	}
	job := &Job{OmitHistory: !keepRequestHistory(&a.state, p), ID: secret(), PairingID: p.ID, Request: in, RequestHash: hash, LeaseID: lease, Status: "awaiting_approval", Billing: "none", Revision: 1, CreatedAt: a.now(), Plan: plan, ActionHash: plan.ActionHash, ActionTitle: title}
	for _, id := range in.Inputs {
		if input := a.inputs[id]; input != nil {
			job.InputNames = append(job.InputNames, input.Name)
		}
	}
	if p.Automatic {
		job.Status = "queued"
	}
	if err := a.update(func(s *State) error {
		if in.ConversationID == "" {
			if err := admitDefinitions(s, in.Tools); err != nil {
				return err
			}
		}
		if err := makeJobRoom(s); err != nil {
			return err
		}
		// Reserve first. A failed job-state save consumes the key rather than
		// risking execution again after a crash or ambiguous submission.
		if err := a.reserveReceipt(key, Receipt{JobID: job.ID, Hash: hash, ToolSetHash: in.ToolSetHash, LeaseHash: digest(lease)}); err != nil {
			return err
		}
		if len(in.Inputs) > 0 || plan.Kind == "image-generate" || plan.Kind == "image-edit" || plan.Kind == "command" && a.actions[plan.Action].Files {
			markFileScope(s, job)
		}
		s.Jobs = append(s.Jobs, job)
		if c := s.Conversations[in.ConversationID]; c != nil {
			c.LastJobID = job.ID
			c.Turns++
			job.Turn = c.Turns
			c.UpdatedAt = a.now()
		}
		return nil
	}); err != nil {
		return nil, false, err
	}
	return findJob(&a.state, job.ID), false, nil
}
