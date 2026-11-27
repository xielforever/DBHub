package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGuardedDeliveryCancellationDoesNotBecomePermissionConflict(t *testing.T) {
	for _, phase := range []string{"before", "during-error", "during-success", "deadline", "denied", "valid"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if phase == "before" {
				cancel()
			}
			if phase == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer stop()
			}
			response := httptest.NewRecorder()
			response.Header().Set("X-Private-Result", "private")
			response.WriteHeader(201)
			response.WriteString("private-business-result")
			out := httptest.NewRecorder()
			calls := 0
			verify := func() error {
				calls++
				if strings.HasPrefix(phase, "during") {
					cancel()
				}
				if phase == "during-error" {
					return context.Canceled
				}
				if phase == "denied" {
					return errors.New("authorization changed")
				}
				return nil
			}
			deliverGuardedResponse(out, httptest.NewRequest("POST", "/test", nil).WithContext(ctx), response, verify)
			want := 499
			if phase == "deadline" {
				want = 504
			}
			if phase == "denied" {
				want = 409
			}
			if phase == "valid" {
				want = 201
			}
			if out.Code != want {
				t.Fatalf("status %d, want %d", out.Code, want)
			}
			if phase != "valid" && (strings.Contains(out.Body.String(), "private-business-result") || out.Header().Get("X-Private-Result") != "") {
				t.Fatal("failed delivery leaked buffered data or headers")
			}
			if phase == "valid" && out.Body.String() != "private-business-result" {
				t.Fatal("valid result lost")
			}
			if (phase == "before" || phase == "deadline") && calls != 0 {
				t.Fatal("verified an already stopped request")
			}
			if phase != "before" && phase != "deadline" && calls != 1 {
				t.Fatal("authorization verification skipped")
			}
		})
	}
}
