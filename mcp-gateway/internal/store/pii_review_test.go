package store

import (
	"testing"
	"time"
)

func TestPIIReviewApprovalExpires(t *testing.T) {
	s := NewMemoryStore()
	s.AddPIIReview(PIIReview{ID: "old", WorkspaceID: "w", UserID: "u", PublicName: "tool", Direction: "input", PayloadHash: "hash", Status: "pending", CreatedAt: time.Now().Add(-25 * time.Hour)})
	if s.ApprovePIIReview("w", "old") {
		t.Fatal("expired request was approved")
	}
	if reviews := s.ListPIIReviews("w"); len(reviews) != 1 || reviews[0].Status != "expired" {
		t.Fatalf("expired review status: %+v", reviews)
	}
	s.AddPIIReview(PIIReview{ID: "approved-old", WorkspaceID: "w", UserID: "u", PublicName: "tool", Direction: "input", PayloadHash: "hash", Status: "approved", CreatedAt: time.Now().Add(-25 * time.Hour)})
	if s.ConsumePIIReview("w", "u", "tool", "input", "hash") {
		t.Fatal("expired approval was consumed")
	}
}
