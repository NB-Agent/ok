// Package evolution — Gap 4: Evolution Control Protocol (ECP).
//
// ECP is the protocol for cross-instance knowledge sharing. It defines the
// message types, serialization, and aggregation primitives that enable
// multiple agent instances to share learned skills and patterns. Without
// ECP, evolution is siloed to a single device. With ECP, the agent's
// knowledge compounds across every user and every session.
//
// Protocol layers:
//
//	ECP/1.0 — Core types and serialization (this file)
//	ECP/1.1 — Federation transport (transport.go)
//
// This file implements ECP/1.0: the type system and transport-neutral
// message definitions.
package evolution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// ─── ECP message types ────────────────────────────────────────────────────

// ECPSkillPacket is a skill packaged for transmission between agent instances.
// It carries the skill body, metadata, and integrity proof.
type ECPSkillPacket struct {
	// Header — routing and integrity.
	ID        string    `json:"id"`        // unique packet ID (SHA-256 of body)
	Version   string    `json:"version"`   // ECP protocol version ("1.0")
	CreatedAt time.Time `json:"createdAt"` // when this packet was created

	// Origin — where the skill came from.
	OriginInstance string `json:"originInstance"` // agent instance identifier
	OriginUserHash string `json:"originUserHash"` // SHA-256 of user ID (privacy-preserving)
	OriginOS       string `json:"originOS"`       // "windows/amd64", "linux/arm64"
	OriginVersion  string `json:"originVersion"`  // agent version that generated this

	// Payload — the skill itself.
	SkillName        string   `json:"skillName"`
	SkillDescription string   `json:"skillDescription"`
	SkillBody        string   `json:"skillBody"`
	Patterns         []string `json:"patterns"`   // detected patterns that triggered generation
	Confidence       float64  `json:"confidence"` // 0.0–1.0, how confident the generator was

	// Integrity.
	IntegrityHash string `json:"integrityHash"` // SHA-256 of the skill body

	// Privacy flags.
	Shareable  bool     `json:"shareable"`  // can be federated to other instances
	Tags       []string `json:"tags"`       // categorization tags
	Deprecated bool     `json:"deprecated"` // true when superseded by a newer version
}

// ECPKnowledgeUpdate is an aggregated knowledge snapshot from a peer instance.
// It carries multiple skill packets and usage statistics.
type ECPKnowledgeUpdate struct {
	ID           string           `json:"id"`
	PeerInstance string           `json:"peerInstance"`
	GeneratedAt  time.Time        `json:"generatedAt"`
	Skills       []ECPSkillPacket `json:"skills"`
	Stats        ECPPeerStats     `json:"stats"`
	SequenceNum  int64            `json:"sequenceNum"` // monotonic update counter
}

// ECPPeerStats carries aggregate statistics about a peer's evolution.
type ECPPeerStats struct {
	TotalSkills      int      `json:"totalSkills"`
	TotalExtractions int64    `json:"totalExtractions"`
	AvgConfidence    float64  `json:"avgConfidence"`
	UptimeHours      float64  `json:"uptimeHours"`
	TopWorkflows     []string `json:"topWorkflows"` // most frequent workflow names
}

// ECPManifest describes what knowledge a peer has available for sharing.
// Used during peer discovery to decide what to sync.
type ECPManifest struct {
	Instance    string    `json:"instance"`
	Version     string    `json:"version"`
	GeneratedAt time.Time `json:"generatedAt"`
	SequenceNum int64     `json:"sequenceNum"`
	SkillCount  int       `json:"skillCount"`
	SkillNames  []string  `json:"skillNames"` // for lightweight discovery
	TopTags     []string  `json:"topTags"`    // for topic-based filtering
	LastUpdate  time.Time `json:"lastUpdate"`
}

// ECPMergeResult describes the outcome of merging knowledge from a peer.
type ECPMergeResult struct {
	NewSkills      int      `json:"newSkills"`      // skills installed for the first time
	UpdatedSkills  int      `json:"updatedSkills"`  // skills that were refreshed
	RejectedSkills int      `json:"rejectedSkills"` // skills rejected (unsafe, duplicate, etc.)
	Conflicts      []string `json:"conflicts"`      // skills that conflicted with local versions
}

// ─── Packet creation ──────────────────────────────────────────────────────

// NewECPSkillPacket creates a new skill packet ready for transmission.
// The originUserHash is a SHA-256 of the user ID — the raw ID never leaves
// the local instance.
func NewECPSkillPacket(
	instance, userID, osArch, agentVersion string,
	name, description, body string,
	patterns []string, confidence float64,
) ECPSkillPacket {
	hash := sha256.Sum256([]byte(body))
	userHash := sha256.Sum256([]byte(userID))

	return ECPSkillPacket{
		ID:               hex.EncodeToString(hash[:8]),
		Version:          "1.0",
		CreatedAt:        time.Now().UTC(),
		OriginInstance:   instance,
		OriginUserHash:   hex.EncodeToString(userHash[:16]),
		OriginOS:         osArch,
		OriginVersion:    agentVersion,
		SkillName:        name,
		SkillDescription: description,
		SkillBody:        body,
		Patterns:         patterns,
		Confidence:       confidence,
		IntegrityHash:    hex.EncodeToString(hash[:]),
		Shareable:        true,
		Tags:             extractTags(patterns),
	}
}

// Verify checks the packet's integrity hash against its skill body.
func (p ECPSkillPacket) Verify() error {
	hash := sha256.Sum256([]byte(p.SkillBody))
	expected := hex.EncodeToString(hash[:])
	if p.IntegrityHash != expected {
		return fmt.Errorf("ecp: integrity check failed for skill %q: hash mismatch", p.SkillName)
	}
	return nil
}

// ─── Serialization ────────────────────────────────────────────────────────

// Marshal serializes a skill packet to JSON.
func (p ECPSkillPacket) Marshal() ([]byte, error) {
	return json.Marshal(p)
}

// UnmarshalECPPacket deserializes a skill packet from JSON.
func UnmarshalECPPacket(data []byte) (ECPSkillPacket, error) {
	var p ECPSkillPacket
	if err := json.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("ecp: unmarshal: %w", err)
	}
	return p, nil
}

// MarshalKnowledgeUpdate serializes a knowledge update.
func (u ECPKnowledgeUpdate) Marshal() ([]byte, error) {
	return json.Marshal(u)
}

// UnmarshalKnowledgeUpdate deserializes a knowledge update.
func UnmarshalKnowledgeUpdate(data []byte) (ECPKnowledgeUpdate, error) {
	var u ECPKnowledgeUpdate
	if err := json.Unmarshal(data, &u); err != nil {
		return u, fmt.Errorf("ecp: unmarshal update: %w", err)
	}
	return u, nil
}

// MarshalManifest serializes a manifest.
func (m ECPManifest) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// ─── Knowledge aggregation ────────────────────────────────────────────────

// MergeKnowledge aggregates skills from a peer update into a local skill set.
// Returns the merge result describing what was installed/updated/rejected.
//
// acceptFn is called for each skill before installation. If it returns false,
// the skill is rejected. This allows the caller to enforce acceptance policies
// (e.g., only accept skills with confidence >= 0.7, only from trusted peers).
func MergeKnowledge(
	update ECPKnowledgeUpdate,
	existingNames map[string]bool,
	acceptFn func(ECPSkillPacket) bool,
	installFn func(ECPSkillPacket) error,
) ECPMergeResult {
	var result ECPMergeResult

	for _, sk := range update.Skills {
		// 1. Integrity check.
		if err := sk.Verify(); err != nil {
			result.RejectedSkills++
			result.Conflicts = append(result.Conflicts,
				fmt.Sprintf("%s: integrity failed", sk.SkillName))
			continue
		}

		// 2. Acceptance policy.
		if acceptFn != nil && !acceptFn(sk) {
			result.RejectedSkills++
			result.Conflicts = append(result.Conflicts,
				fmt.Sprintf("%s: rejected by policy", sk.SkillName))
			continue
		}

		// 3. Duplicate detection.
		if existingNames[sk.SkillName] {
			result.UpdatedSkills++
		} else {
			result.NewSkills++
			existingNames[sk.SkillName] = true
		}

		// 4. Install.
		if installFn != nil {
			if err := installFn(sk); err != nil {
				result.Conflicts = append(result.Conflicts,
					fmt.Sprintf("%s: install failed: %v", sk.SkillName, err))
			}
		}
	}

	return result
}

// DefaultAcceptPolicy accepts skills with confidence >= 0.6 and no
// dangerous patterns in the body.
func DefaultAcceptPolicy(p ECPSkillPacket) bool {
	if p.Confidence < 0.6 {
		return false
	}
	if err := ValidateSkillSafety(p.SkillBody); err != nil {
		return false
	}
	return true
}

// ─── Helpers ──────────────────────────────────────────────────────────────

// extractTags derives categorization tags from the detected patterns.
func extractTags(patterns []string) []string {
	var tags []string
	seen := make(map[string]bool)

	for _, p := range patterns {
		var tag string
		switch {
		case stringsPrefixAny(p, "workflow:tdd", "fingerprint:tool:bash"):
			tag = "testing"
		case stringsPrefixAny(p, "workflow:audit", "workflow:search"):
			tag = "code-review"
		case stringsPrefixAny(p, "workflow:git-commit", "sequence:git→"):
			tag = "version-control"
		case stringsPrefixAny(p, "workflow:build-verify-deploy"):
			tag = "deployment"
		case stringsPrefixAny(p, "workflow:dependency"):
			tag = "dependencies"
		case stringsPrefixAny(p, "workflow:research"):
			tag = "research"
		case stringsPrefixAny(p, "workflow:debug"):
			tag = "debugging"
		default:
			continue
		}
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	if len(tags) == 0 {
		tags = append(tags, "general")
	}
	return tags
}

func stringsPrefixAny(s string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
