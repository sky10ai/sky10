package rpc

import (
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	skysecrets "github.com/sky10/sky10/pkg/secrets"
)

const (
	secretReferenceManager = "messaging"
	secretReferenceKind    = "messaging-connection"
	secretReferenceRoute   = "/settings/messaging"
)

// ConnectionLister is the minimal slice of the messaging store used by the
// secrets reference resolver.
type ConnectionLister interface {
	ListConnections() []messaging.Connection
}

// SecretReferenceResolver implements secrets.ReferenceResolver by matching a
// secret summary against the credential refs of every messaging connection.
type SecretReferenceResolver struct {
	Connections ConnectionLister
}

// References returns one entry per messaging connection that references the
// given secret by name. Returns nil when there are no matches.
func (r SecretReferenceResolver) References(summary skysecrets.SecretSummary) []skysecrets.SecretReference {
	if r.Connections == nil {
		return nil
	}
	name := strings.TrimSpace(summary.Name)
	if name == "" {
		return nil
	}
	var refs []skysecrets.SecretReference
	for _, connection := range r.Connections.ListConnections() {
		ref := strings.TrimSpace(connection.Auth.CredentialRef)
		if ref == "" || ref != name {
			continue
		}
		subject := strings.TrimSpace(connection.Label)
		if subject == "" {
			subject = string(connection.ID)
		}
		refs = append(refs, skysecrets.SecretReference{
			Kind:    secretReferenceKind,
			Manager: secretReferenceManager,
			Subject: subject,
			Detail:  string(connection.AdapterID),
			Route:   secretReferenceRoute,
		})
	}
	return refs
}
