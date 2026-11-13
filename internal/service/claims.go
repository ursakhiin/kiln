package service

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
)

// claim is an agent's hold on a task. Claims live only in memory: when the
// daemon restarts, every IN_PROGRESS task is unclaimed, so next_task offers
// it as abandoned.
type claim struct {
	agentID string
	expire  time.Time
}

// expireClaims ends every claim whose expiry is not after now. Expiry is
// checked lazily, on each call, rather than by a goroutine.
func (s *Service) expireClaims(now time.Time) {
	for taskID, c := range s.claims {
		if !c.expire.After(now) {
			s.endClaim(taskID)
		}
	}
}

// renewClaims extends every claim held by agentID to now + ClaimTTL.
func (s *Service) renewClaims(agentID string, now time.Time) {
	for _, c := range s.claims {
		if c.agentID == agentID {
			c.expire = now.Add(s.claimTTL)
		}
	}
}

// startClaim gives agentID the claim on the task taskID, in feature
// featureID, and makes that feature the agent's affinity.
func (s *Service) startClaim(taskID, featureID, agentID string) {
	s.claims[taskID] = &claim{agentID: agentID, expire: s.now().Add(s.claimTTL)}
	s.affinity[agentID] = featureID
}

// endClaim is the single place a claim ends: on expiry, release, a status
// change out of IN_PROGRESS, and so on. It does nothing if taskID is
// unclaimed.
func (s *Service) endClaim(taskID string) {
	if _, ok := s.claims[taskID]; !ok {
		return
	}
	delete(s.claims, taskID)
}

// claimOf returns the claim on item, if any.
func (s *Service) claimOf(item *kilnv1.Item) *kilnv1.Claim {
	c, ok := s.claims[item.GetId()]
	if !ok {
		return nil
	}
	return &kilnv1.Claim{AgentId: c.agentID, ExpireTime: timestamppb.New(c.expire)}
}

// holds reports whether caller is the agent holding the claim on taskID.
func (s *Service) holds(caller Caller, taskID string) bool {
	c, ok := s.claims[taskID]
	return ok && caller.Actor == kilnv1.ActorType_ACTOR_TYPE_AGENT && c.agentID == caller.AgentID
}
