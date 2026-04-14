package link

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ipfs/go-cid"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	mh "github.com/multiformats/go-multihash"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
)

const (
	privateNetworkNamespace = "sky10-private-network"
	privateNetworkVersion   = "v1"

	membershipSchema = "sky10.private-network.membership.v1"
	presenceSchema   = "sky10.private-network.presence.v1"

	defaultPresenceTTL = 10 * time.Minute
)

type privateNetworkKey struct {
	Kind         string
	Identity     string
	DevicePubKey string
}

type MembershipDevice struct {
	PublicKey string    `json:"public_key"`
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty"`
	AddedAt   time.Time `json:"added_at"`
}

type RevokedDevice struct {
	PublicKey string    `json:"public_key"`
	RevokedAt time.Time `json:"revoked_at"`
}

// MembershipRecord is the durable private-network membership set published to
// the DHT and mirrored to Nostr.
type MembershipRecord struct {
	Schema    string             `json:"schema"`
	Identity  string             `json:"identity"`
	Revision  int64              `json:"revision"`
	UpdatedAt time.Time          `json:"updated_at"`
	Devices   []MembershipDevice `json:"devices"`
	Revoked   []RevokedDevice    `json:"revoked,omitempty"`
	Signature []byte             `json:"signature"`
}

// PresenceRecord is the device-scoped private-network presence record.
type PresenceRecord struct {
	Schema       string    `json:"schema"`
	Identity     string    `json:"identity"`
	DevicePubKey string    `json:"device_pubkey"`
	PeerID       string    `json:"peer_id"`
	Multiaddrs   []string  `json:"multiaddrs"`
	PublishedAt  time.Time `json:"published_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Version      string    `json:"version,omitempty"`
	Signature    []byte    `json:"signature"`
}

func membershipDHTKey(identity string) string {
	return "/" + privateNetworkNamespace + "/" + privateNetworkVersion + "/membership/" + identity
}

func presenceDHTKey(identity, devicePubKey string) string {
	return "/" + privateNetworkNamespace + "/" + privateNetworkVersion + "/presence/" + identity + "/" + strings.ToLower(devicePubKey)
}

func providerCIDForKey(key string) cid.Cid {
	sum, err := mh.Sum([]byte(key), mh.SHA2_256, -1)
	if err != nil {
		panic(fmt.Sprintf("computing provider CID: %v", err))
	}
	return cid.NewCidV1(cid.Raw, sum)
}

func membershipProviderCID(identity string) cid.Cid {
	return providerCIDForKey(membershipDHTKey(identity))
}

func presenceProviderCID(identity, devicePubKey string) cid.Cid {
	return providerCIDForKey(presenceDHTKey(identity, devicePubKey))
}

func membershipNostrDTag(identity string) string {
	return privateNetworkNamespace + ":" + privateNetworkVersion + ":membership:" + identity
}

func presenceNostrDTag(identity, devicePubKey string) string {
	return privateNetworkNamespace + ":" + privateNetworkVersion + ":presence:" + identity + ":" + strings.ToLower(devicePubKey)
}

func parsePrivateNetworkKey(key string) (*privateNetworkKey, error) {
	parts := strings.Split(strings.Trim(key, "/"), "/")
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid private-network key: %s", key)
	}
	if parts[0] != privateNetworkNamespace {
		return nil, fmt.Errorf("unexpected private-network namespace: %s", key)
	}
	if parts[1] != privateNetworkVersion {
		return nil, fmt.Errorf("unexpected private-network version: %s", key)
	}

	out := &privateNetworkKey{
		Kind:     parts[2],
		Identity: parts[3],
	}
	switch out.Kind {
	case "membership":
		if len(parts) != 4 {
			return nil, fmt.Errorf("invalid membership key: %s", key)
		}
	case "presence":
		if len(parts) != 5 {
			return nil, fmt.Errorf("invalid presence key: %s", key)
		}
		out.DevicePubKey = strings.ToLower(parts[4])
	default:
		return nil, fmt.Errorf("unknown private-network record kind: %s", key)
	}

	return out, nil
}

func decodeDevicePubKeyHex(devicePubKey string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(strings.ToLower(devicePubKey))
	if err != nil {
		return nil, fmt.Errorf("decoding device public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid device public key length: %d", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

func canonicalMembershipDevices(devices []MembershipDevice) []MembershipDevice {
	out := append([]MembershipDevice(nil), devices...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].PublicKey < out[j].PublicKey
	})
	return out
}

func canonicalRevokedDevices(revoked []RevokedDevice) []RevokedDevice {
	out := append([]RevokedDevice(nil), revoked...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].PublicKey < out[j].PublicKey
	})
	return out
}

func (r *MembershipRecord) canonicalPayload() ([]byte, error) {
	payload := struct {
		Schema    string             `json:"schema"`
		Identity  string             `json:"identity"`
		Revision  int64              `json:"revision"`
		UpdatedAt time.Time          `json:"updated_at"`
		Devices   []MembershipDevice `json:"devices"`
		Revoked   []RevokedDevice    `json:"revoked,omitempty"`
	}{
		Schema:    r.Schema,
		Identity:  r.Identity,
		Revision:  r.Revision,
		UpdatedAt: r.UpdatedAt.UTC(),
		Devices:   canonicalMembershipDevices(r.Devices),
		Revoked:   canonicalRevokedDevices(r.Revoked),
	}
	return json.Marshal(payload)
}

func (r *MembershipRecord) Sign(identityPriv ed25519.PrivateKey) error {
	payload, err := r.canonicalPayload()
	if err != nil {
		return fmt.Errorf("computing membership sign payload: %w", err)
	}
	r.Signature = skykey.Sign(payload, identityPriv)
	return nil
}

func (r *MembershipRecord) Validate(key string) error {
	keyParts, err := parsePrivateNetworkKey(key)
	if err != nil {
		return err
	}
	if keyParts.Kind != "membership" {
		return fmt.Errorf("membership record used with non-membership key")
	}
	if r.Schema != membershipSchema {
		return fmt.Errorf("invalid membership schema: %q", r.Schema)
	}
	if r.Identity != keyParts.Identity {
		return fmt.Errorf("membership identity mismatch")
	}
	identityKey, err := skykey.ParseAddress(r.Identity)
	if err != nil {
		return fmt.Errorf("parsing membership identity: %w", err)
	}
	if r.Revision <= 0 {
		return fmt.Errorf("membership revision must be positive")
	}
	if len(r.Signature) == 0 {
		return fmt.Errorf("missing membership signature")
	}

	active := make(map[string]struct{}, len(r.Devices))
	for _, device := range r.Devices {
		if _, err := decodeDevicePubKeyHex(device.PublicKey); err != nil {
			return err
		}
		if _, ok := active[device.PublicKey]; ok {
			return fmt.Errorf("duplicate membership device: %s", device.PublicKey)
		}
		active[device.PublicKey] = struct{}{}
	}
	for _, revoked := range r.Revoked {
		if _, err := decodeDevicePubKeyHex(revoked.PublicKey); err != nil {
			return err
		}
		if _, ok := active[revoked.PublicKey]; ok {
			return fmt.Errorf("device appears in active and revoked sets: %s", revoked.PublicKey)
		}
	}

	payload, err := r.canonicalPayload()
	if err != nil {
		return err
	}
	if !skykey.Verify(payload, r.Signature, identityKey.PublicKey) {
		return fmt.Errorf("membership signature invalid")
	}
	return nil
}

func (r *MembershipRecord) ToManifest(identity *skykey.Key) (*id.DeviceManifest, error) {
	if err := r.Validate(membershipDHTKey(r.Identity)); err != nil {
		return nil, err
	}
	manifest := &id.DeviceManifest{
		Identity:  r.Identity,
		UpdatedAt: r.UpdatedAt.UTC(),
		Devices:   make([]id.DeviceEntry, 0, len(r.Devices)),
	}
	for _, device := range canonicalMembershipDevices(r.Devices) {
		pub, err := decodeDevicePubKeyHex(device.PublicKey)
		if err != nil {
			return nil, err
		}
		manifest.Devices = append(manifest.Devices, id.DeviceEntry{
			PublicKey: []byte(pub),
			Name:      device.Name,
			Role:      id.CanonicalDeviceRole(device.Role),
			AddedAt:   device.AddedAt.UTC(),
		})
	}
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		return nil, fmt.Errorf("signing local manifest cache: %w", err)
	}
	return manifest, nil
}

func membershipRecordFromManifest(manifest *id.DeviceManifest) *MembershipRecord {
	if manifest == nil {
		return nil
	}
	devices := make([]MembershipDevice, 0, len(manifest.Devices))
	for _, device := range manifest.Devices {
		devices = append(devices, MembershipDevice{
			PublicKey: strings.ToLower(hex.EncodeToString(device.PublicKey)),
			Name:      device.Name,
			Role:      id.CanonicalDeviceRole(device.Role),
			AddedAt:   device.AddedAt.UTC(),
		})
	}
	updatedAt := manifest.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	revision := updatedAt.UnixNano()
	if revision <= 0 {
		revision = 1
	}
	return &MembershipRecord{
		Schema:    membershipSchema,
		Identity:  manifest.Identity,
		Revision:  revision,
		UpdatedAt: updatedAt,
		Devices:   devices,
	}
}

func canonicalMultiaddrs(addrs []string) []string {
	out := append([]string(nil), addrs...)
	sort.Strings(out)
	if len(out) == 0 {
		return out
	}
	uniq := out[:1]
	for _, addr := range out[1:] {
		if addr != uniq[len(uniq)-1] {
			uniq = append(uniq, addr)
		}
	}
	return uniq
}

func (r *PresenceRecord) canonicalPayload() ([]byte, error) {
	payload := struct {
		Schema       string    `json:"schema"`
		Identity     string    `json:"identity"`
		DevicePubKey string    `json:"device_pubkey"`
		PeerID       string    `json:"peer_id"`
		Multiaddrs   []string  `json:"multiaddrs"`
		PublishedAt  time.Time `json:"published_at"`
		ExpiresAt    time.Time `json:"expires_at"`
		Version      string    `json:"version,omitempty"`
	}{
		Schema:       r.Schema,
		Identity:     r.Identity,
		DevicePubKey: strings.ToLower(r.DevicePubKey),
		PeerID:       r.PeerID,
		Multiaddrs:   canonicalMultiaddrs(r.Multiaddrs),
		PublishedAt:  r.PublishedAt.UTC(),
		ExpiresAt:    r.ExpiresAt.UTC(),
		Version:      r.Version,
	}
	return json.Marshal(payload)
}

func (r *PresenceRecord) Sign(devicePriv ed25519.PrivateKey) error {
	payload, err := r.canonicalPayload()
	if err != nil {
		return fmt.Errorf("computing presence sign payload: %w", err)
	}
	r.Signature = skykey.Sign(payload, devicePriv)
	return nil
}

func (r *PresenceRecord) Validate(key string) error {
	keyParts, err := parsePrivateNetworkKey(key)
	if err != nil {
		return err
	}
	if keyParts.Kind != "presence" {
		return fmt.Errorf("presence record used with non-presence key")
	}
	if r.Schema != presenceSchema {
		return fmt.Errorf("invalid presence schema: %q", r.Schema)
	}
	if r.Identity != keyParts.Identity {
		return fmt.Errorf("presence identity mismatch")
	}
	if strings.ToLower(r.DevicePubKey) != keyParts.DevicePubKey {
		return fmt.Errorf("presence device public key mismatch")
	}
	if len(r.Signature) == 0 {
		return fmt.Errorf("missing presence signature")
	}
	if !r.ExpiresAt.After(r.PublishedAt) {
		return fmt.Errorf("presence expiry must be later than publish time")
	}
	if _, err := skykey.ParseAddress(r.Identity); err != nil {
		return fmt.Errorf("parsing presence identity: %w", err)
	}

	devicePub, err := decodeDevicePubKeyHex(r.DevicePubKey)
	if err != nil {
		return err
	}
	expectedPeerID, err := PeerIDFromPubKey(devicePub)
	if err != nil {
		return err
	}
	if r.PeerID != expectedPeerID.String() {
		return fmt.Errorf("presence peer ID does not match device public key")
	}
	for _, addr := range r.Multiaddrs {
		info, err := addrInfoFromMultiaddrStrings([]string{addr})
		if err != nil {
			return fmt.Errorf("invalid presence multiaddr %q: %w", addr, err)
		}
		if info.ID != expectedPeerID {
			return fmt.Errorf("presence multiaddr peer ID mismatch")
		}
	}

	payload, err := r.canonicalPayload()
	if err != nil {
		return err
	}
	if !skykey.Verify(payload, r.Signature, devicePub) {
		return fmt.Errorf("presence signature invalid")
	}
	return nil
}

func (r *PresenceRecord) Usable(membership *MembershipRecord, now time.Time) bool {
	if membership == nil || now.After(r.ExpiresAt) {
		return false
	}
	devicePubKey := strings.ToLower(r.DevicePubKey)
	for _, revoked := range membership.Revoked {
		if strings.ToLower(revoked.PublicKey) == devicePubKey {
			return false
		}
	}
	for _, device := range membership.Devices {
		if strings.ToLower(device.PublicKey) == devicePubKey {
			return true
		}
	}
	return false
}

func currentPresenceRecord(n *Node, ttl time.Duration) *PresenceRecord {
	return currentPresenceRecordWithMultiaddrs(n, ttl, HostMultiaddrs(n))
}

func currentPresenceRecordWithMultiaddrs(n *Node, ttl time.Duration, addrs []string) *PresenceRecord {
	if ttl <= 0 {
		ttl = defaultPresenceTTL
	}
	now := time.Now().UTC()
	return &PresenceRecord{
		Schema:       presenceSchema,
		Identity:     n.Address(),
		DevicePubKey: strings.ToLower(hex.EncodeToString(n.bundle.Device.PublicKey)),
		PeerID:       n.peerID.String(),
		Multiaddrs:   append([]string(nil), addrs...),
		PublishedAt:  now,
		ExpiresAt:    now.Add(ttl),
		Version:      n.version,
	}
}

func (n *Node) CurrentMembershipRecord() (*MembershipRecord, error) {
	rec := membershipRecordFromManifest(n.bundle.Manifest)
	if rec == nil {
		return nil, fmt.Errorf("missing membership manifest")
	}
	if err := rec.Sign(n.bundle.Identity.PrivateKey); err != nil {
		return nil, err
	}
	return rec, nil
}

func (n *Node) CurrentPresenceRecord(ttl time.Duration) (*PresenceRecord, error) {
	rec := currentPresenceRecord(n, ttl)
	if err := rec.Sign(n.bundle.Device.PrivateKey); err != nil {
		return nil, err
	}
	return rec, nil
}

// CurrentPresenceRecordForPublish builds a signed presence record using the
// host's current dialable addresses, reordered by a short public STUN probe so
// direct dial hints prefer the most likely transport.
func (n *Node) CurrentPresenceRecordForPublish(ctx context.Context, ttl time.Duration) (*PresenceRecord, error) {
	rec := currentPresenceRecordWithMultiaddrs(n, ttl, PublishedHostMultiaddrs(ctx, n))
	if err := rec.Sign(n.bundle.Device.PrivateKey); err != nil {
		return nil, err
	}
	return rec, nil
}

func compareMembershipRecords(a, b *MembershipRecord) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch {
	case a.Revision > b.Revision:
		return 1
	case a.Revision < b.Revision:
		return -1
	case a.UpdatedAt.After(b.UpdatedAt):
		return 1
	case a.UpdatedAt.Before(b.UpdatedAt):
		return -1
	}
	ap, _ := a.canonicalPayload()
	bp, _ := b.canonicalPayload()
	return bytes.Compare(ap, bp)
}

func comparePresenceRecords(a, b *PresenceRecord) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch {
	case a.PublishedAt.After(b.PublishedAt):
		return 1
	case a.PublishedAt.Before(b.PublishedAt):
		return -1
	case a.ExpiresAt.After(b.ExpiresAt):
		return 1
	case a.ExpiresAt.Before(b.ExpiresAt):
		return -1
	}
	ap, _ := a.canonicalPayload()
	bp, _ := b.canonicalPayload()
	return bytes.Compare(ap, bp)
}

func selectBestMembership(records ...*MembershipRecord) *MembershipRecord {
	var best *MembershipRecord
	for _, rec := range records {
		if compareMembershipRecords(rec, best) > 0 {
			best = rec
		}
	}
	return best
}

func selectBestPresence(records ...*PresenceRecord) *PresenceRecord {
	var best *PresenceRecord
	for _, rec := range records {
		if comparePresenceRecords(rec, best) > 0 {
			best = rec
		}
	}
	return best
}

// initDHT initializes the Kademlia DHT on the node. Only called in
// network mode.
func (n *Node) initDHT(ctx context.Context) error {
	bootstrapPeers := n.config.BootstrapPeers
	if bootstrapPeers == nil {
		bootstrapPeers = dht.GetDefaultBootstrapPeerAddrInfos()
	}

	opts := []dht.Option{
		dht.Mode(dht.ModeAutoServer),
	}
	if len(bootstrapPeers) > 0 {
		opts = append(opts, dht.BootstrapPeers(bootstrapPeers...))
	}

	d, err := dht.New(
		ctx,
		n.host,
		opts...,
	)
	if err != nil {
		return fmt.Errorf("creating DHT: %w", err)
	}
	if err := d.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrapping DHT: %w", err)
	}
	n.dht = d
	return nil
}

func (n *Node) PublishMembershipProvider(ctx context.Context, identity string) error {
	if n.dht == nil {
		return fmt.Errorf("DHT not initialized (network mode required)")
	}
	return n.dht.Provide(ctx, membershipProviderCID(identity), true)
}

func (n *Node) PublishPresenceProvider(ctx context.Context, identity, devicePubKey string) error {
	if n.dht == nil {
		return fmt.Errorf("DHT not initialized (network mode required)")
	}
	return n.dht.Provide(ctx, presenceProviderCID(identity, devicePubKey), true)
}

func (n *Node) PublishRecord(ctx context.Context) error {
	membership, err := n.CurrentMembershipRecord()
	if err != nil {
		return err
	}
	if err := n.PublishMembershipProvider(ctx, membership.Identity); err != nil {
		return err
	}

	presence, err := n.CurrentPresenceRecord(defaultPresenceTTL)
	if err != nil {
		return err
	}
	return n.PublishPresenceProvider(ctx, presence.Identity, presence.DevicePubKey)
}

func addrInfoFromMultiaddrStrings(addrs []string) (*peer.AddrInfo, error) {
	var info *peer.AddrInfo
	for _, addr := range canonicalMultiaddrs(addrs) {
		multiaddr, err := parseP2PMultiaddr(addr)
		if err != nil {
			return nil, err
		}
		next, err := peer.AddrInfoFromP2pAddr(multiaddr)
		if err != nil {
			return nil, err
		}
		if info == nil {
			info = &peer.AddrInfo{ID: next.ID, Addrs: append([]ma.Multiaddr(nil), next.Addrs...)}
			continue
		}
		if info.ID != next.ID {
			return nil, fmt.Errorf("mixed peer IDs in multiaddrs")
		}
		info.Addrs = append(info.Addrs, next.Addrs...)
	}
	if info == nil {
		return nil, fmt.Errorf("no multiaddrs")
	}
	return info, nil
}

func parseP2PMultiaddr(addr string) (ma.Multiaddr, error) {
	return ma.NewMultiaddr(addr)
}
