package reconciler

import (
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/store"
)

// PendingPostHandler receives a live pending post selected for reconciliation.
type PendingPostHandler func(store.PendingPost)

// RouteKeyFunc returns the rate-limit coalescing key for a pending post.
type RouteKeyFunc func(store.PendingPost) (string, bool)

// PostReconciler turns scans or watcher wake-ups into idempotent pending-post work.
type PostReconciler struct {
	CoalesceRateLimitedRoutes bool
	RouteKey                  RouteKeyFunc
}

// ReconcileSessionDirs scans session post/ directories and dispatches selected posts.
func (r PostReconciler) ReconcileSessionDirs(sessionDirs []string, handle PendingPostHandler) (int, error) {
	posts, err := store.ListPendingPosts(sessionDirs)
	if err != nil {
		return 0, err
	}
	return r.ReconcilePosts(posts, handle), nil
}

// ReconcilePath dispatches one concrete post/ path after a watcher wake-up.
func (r PostReconciler) ReconcilePath(path string, handle PendingPostHandler) (bool, error) {
	post, ok := store.PostFromPath(path)
	if !ok {
		return false, nil
	}
	if _, err := os.Stat(post.Path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	handle(post)
	return true, nil
}

// ReconcilePosts dispatches an already-scanned pending-post set.
func (r PostReconciler) ReconcilePosts(posts []store.PendingPost, handle PendingPostHandler) int {
	seenRoutes := make(map[string]bool)
	dispatched := 0
	for _, post := range posts {
		if r.CoalesceRateLimitedRoutes {
			if route, ok := r.routeKey(post); ok {
				if seenRoutes[route] {
					continue
				}
				seenRoutes[route] = true
			}
		}
		handle(post)
		dispatched++
	}
	return dispatched
}

func (r PostReconciler) routeKey(post store.PendingPost) (string, bool) {
	if r.RouteKey == nil {
		return "", false
	}
	return r.RouteKey(post)
}
