package bridge

// Read-only route/capability admission. No receipt, native session, file or job is
// created. Names/models remain governed by the existing local sharing choices.
type AdmissionRequest struct {
	Request  JobRequest `json:"request"`
	Features []string   `json:"features"`
}
type AdmissionStatus struct {
	Available bool   `json:"available"`
	Kind      string `json:"kind,omitempty"`
	Code      string `json:"code,omitempty"`
}

func (a *App) preflight(p *Pairing, in AdmissionRequest) (AdmissionStatus, error) {
	if err := validateRequest(&in.Request); err != nil {
		return AdmissionStatus{}, fail(400, "invalid_job", err.Error())
	}
	plan, _, err := a.prepareJob(p, &in.Request, nil)
	if err != nil {
		return AdmissionStatus{Code: errorCode(err)}, nil
	}
	kind := "native"
	if plan.Kind == "command" {
		kind = "command"
	}
	if p.Paused || p.Revoked || !a.state.Enabled {
		return AdmissionStatus{Kind: kind, Code: "execution_paused"}, nil
	}
	if kind == "native" {
		if err = a.nativeAvailable(p, plan.Provider, plan.Kind); err != nil {
			return AdmissionStatus{Kind: kind, Code: errorCode(err)}, nil
		}
		for _, feature := range in.Features {
			supported := false
			for _, cap := range a.capabilities(p) {
				if cap.ID != plan.Provider {
					continue
				}
				switch feature {
				case "sessions":
					supported = cap.Sessions
				case "streaming":
					supported = cap.Streaming
				case "text":
					supported = cap.Text
				case "json":
					supported = cap.JSON
				case "imageInput":
					supported = cap.ImageInput
				case "documentInput":
					supported = cap.DocumentInput
				case "imageGeneration":
					supported = cap.ImageGeneration
				case "imageEditing":
					supported = cap.ImageEditing
				case "appTools":
					supported = cap.AppTools
				default:
					return AdmissionStatus{}, fail(400, "invalid_feature", "Unknown required capability.")
				}
			}
			if !supported {
				return AdmissionStatus{Kind: kind, Code: "unsupported_feature"}, nil
			}
		}
	}
	return AdmissionStatus{Available: true, Kind: kind}, nil
}
func errorCode(err error) string {
	if api, ok := err.(*apiError); ok {
		return api.Code
	}
	return "unavailable"
}
