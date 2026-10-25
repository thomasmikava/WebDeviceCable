package bridge

import (
	"testing"
	"time"
)

func TestPairingRequestsNeedLocalApprovalAndAppearOnConnections(t *testing.T) {
	f := setup(t)
	id := secret()
	origin := "https://writer.example"
	input := map[string]any{"id": id, "appId": "writer", "name": "Writer", "appUser": "alice", "origin": origin}
	object(t, f.req("POST", "/api/pairing-requests", origin, "", "", input, false), 403)
	object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", input, false), 401)
	object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", input, true), 201)
	object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", input, true), 200)
	state := object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
	requests := state["pairingRequests"].([]any)
	if len(requests) != 1 || requests[0].(map[string]any)["origin"] != origin {
		t.Fatal(state)
	}
	if len(f.a.state.Pairings) != 0 || len(f.a.state.Invitations) != 0 {
		t.Fatal("request granted access without approval")
	}
	path := "/api/pairing-requests/" + id
	object(t, f.req("POST", path+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	object(t, f.req("POST", path+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	if len(f.a.state.Invitations) != 1 {
		t.Fatal("approval created duplicate invitations")
	}
	state = object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
	if len(state["pairingRequests"].([]any)) != 0 {
		t.Fatal("answered request stayed pending")
	}
	request := object(t, f.req("GET", path, f.origin, "", "", nil, true), 200)
	invitation := request["invitation"].(string)
	claim := object(t, f.req("POST", "/v2/pairings/claim", origin, invitation, "", map[string]string{"appId": "writer", "appUser": "alice"}, false), 201)
	pairing := claim["pairing"].(map[string]any)
	if pairing["automatic"] != false || pairing["mode"] != "restricted" || pairing["origin"] != origin {
		t.Fatal("unsafe initial permissions", pairing)
	}
	object(t, f.req("POST", path+"/decision", f.origin, "", "", map[string]bool{"approve": false}, true), 409)
	input["id"] = secret()
	rejected := object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", input, true), 201)["id"].(string)
	object(t, f.req("POST", "/api/pairing-requests/"+rejected+"/decision", f.origin, "", "", map[string]bool{"approve": false}, true), 200)
	if len(f.a.state.Pairings) != 1 {
		t.Fatal("reject paired the app")
	}
	f.now = f.now.Add(6 * time.Minute)
	object(t, f.req("GET", path, f.origin, "", "", nil, true), 410)
	object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
	if len(f.a.pairingRequests) != 0 {
		t.Fatal("expired request metadata retained")
	}
}
