package workspacetree

import (
	"path/filepath"
	"testing"
)

func testRoot(base string, parts ...string) string {
	all := append([]string{base}, parts...)
	return filepath.Join(all...)
}

func TestNearestParentSkipsMissingIntermediateLevels(t *testing.T) {
	base := t.TempDir()
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo", Root: testRoot(base, "repo")},
		{SessionName: "project", Label: "api", Root: testRoot(base, "repo", "apps", "api")},
	})

	parent, ok, reason := topology.NearestParent("project")
	if !ok {
		t.Fatalf("NearestParent = false/%s, want repo parent", reason)
	}
	if parent.SessionName != "repo" {
		t.Fatalf("parent session = %q, want repo", parent.SessionName)
	}
}

func TestNearestChildrenListsOnlyNearestRegisteredDescendants(t *testing.T) {
	base := t.TempDir()
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo", Root: testRoot(base, "repo")},
		{SessionName: "api", Label: "api", Root: testRoot(base, "repo", "apps", "api")},
		{SessionName: "pkg", Label: "pkg", Root: testRoot(base, "repo", "apps", "api", "pkg")},
		{SessionName: "docs", Label: "docs", Root: testRoot(base, "repo", "docs")},
	})

	children, reason := topology.NearestChildren("repo")
	if reason != FailureNone {
		t.Fatalf("NearestChildren reason = %s, want none", reason)
	}
	got := []string{}
	for _, child := range children {
		got = append(got, child.SessionName)
	}
	want := []string{"api", "docs"}
	if len(got) != len(want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("children = %v, want %v", got, want)
		}
	}

	projectChildren, reason := topology.NearestChildren("api")
	if reason != FailureNone {
		t.Fatalf("NearestChildren(api) reason = %s, want none", reason)
	}
	if len(projectChildren) != 1 || projectChildren[0].SessionName != "pkg" {
		t.Fatalf("project children = %#v, want pkg", projectChildren)
	}
}

func TestDuplicateRegisteredRootsAreReportedAndAmbiguous(t *testing.T) {
	base := t.TempDir()
	topology := Build([]Registration{
		{SessionName: "repo-a", Label: "repo-a", Root: testRoot(base, "repo")},
		{SessionName: "repo-b", Label: "repo-b", Root: testRoot(base, "repo")},
		{SessionName: "project", Label: "api", Root: testRoot(base, "repo", "apps", "api")},
	})

	diagnostics := topology.Diagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one duplicate_root", diagnostics)
	}
	if diagnostics[0].Code != "duplicate_root" {
		t.Fatalf("diagnostic code = %q, want duplicate_root", diagnostics[0].Code)
	}
	if len(diagnostics[0].SessionNames) != 2 {
		t.Fatalf("diagnostic sessions = %#v, want both duplicate sessions", diagnostics[0].SessionNames)
	}

	_, ok, reason := topology.NearestParent("project")
	if ok || reason != FailureAmbiguousRoot {
		t.Fatalf("NearestParent duplicate = ok:%v reason:%s, want ambiguous_root", ok, reason)
	}
	resolution := topology.ResolveAlias("@parent/worker", "project", nil)
	if resolution.Found || resolution.FailureReason != FailureAmbiguousRoot {
		t.Fatalf("ResolveAlias duplicate = %#v, want ambiguous_root", resolution)
	}
}

func TestResolveAliasCompilesToExplicitSessionNodeAddress(t *testing.T) {
	base := t.TempDir()
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo", Root: testRoot(base, "repo")},
		{SessionName: "api", Label: "api", Root: testRoot(base, "repo", "apps", "api")},
		{SessionName: "docs", Label: "docs", Root: testRoot(base, "repo", "docs")},
	})

	parent := topology.ResolveAlias("@parent/orchestrator", "api", nil)
	if !parent.Found || parent.Address != "repo:orchestrator" {
		t.Fatalf("parent alias = %#v, want repo:orchestrator", parent)
	}

	child := topology.ResolveAlias("@child/api/worker", "repo", nil)
	if !child.Found || child.Address != "api:worker" {
		t.Fatalf("child alias = %#v, want api:worker", child)
	}

	byRootID := topology.ResolveAlias("@child/"+childTargetRootID(t, topology, "docs")+"/worker", "repo", nil)
	if !byRootID.Found || byRootID.Address != "docs:worker" {
		t.Fatalf("child alias by root id = %#v, want docs:worker", byRootID)
	}
}

func TestResolveAliasFailuresAreTyped(t *testing.T) {
	base := t.TempDir()
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo", Root: testRoot(base, "repo")},
		{SessionName: "api", Label: "api", Root: testRoot(base, "repo", "apps", "api")},
		{SessionName: "docs", Label: "docs", Root: testRoot(base, "repo", "docs")},
	})

	cases := []struct {
		name    string
		alias   string
		session string
		want    FailureReason
	}{
		{name: "invalid", alias: "@child/api/worker/extra", session: "repo", want: FailureInvalidAlias},
		{name: "unknown source", alias: "@parent/worker", session: "missing", want: FailureUnknownSourceRoot},
		{name: "ambiguous child", alias: "@child/worker", session: "repo", want: FailureAmbiguousChild},
		{name: "no parent", alias: "@parent/worker", session: "repo", want: FailureNoParent},
		{name: "no selected child", alias: "@child/mobile/worker", session: "repo", want: FailureNoChild},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolution := topology.ResolveAlias(tc.alias, tc.session, nil)
			if resolution.Found || resolution.FailureReason != tc.want {
				t.Fatalf("ResolveAlias(%q, %q) = %#v, want %s", tc.alias, tc.session, resolution, tc.want)
			}
		})
	}
}

func TestResolveAliasCanRequireLiveNodeExistence(t *testing.T) {
	base := t.TempDir()
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo", Root: testRoot(base, "repo")},
		{SessionName: "api", Label: "api", Root: testRoot(base, "repo", "apps", "api")},
	})

	resolution := topology.ResolveAlias("@parent/orchestrator", "api", func(address string) bool {
		return address == "repo:messenger"
	})
	if resolution.Found || resolution.FailureReason != FailureUnknownNode || resolution.Address != "repo:orchestrator" {
		t.Fatalf("ResolveAlias nodeExists = %#v, want unknown_node with compiled address", resolution)
	}
}

func childTargetRootID(t *testing.T, topology Topology, sessionName string) string {
	t.Helper()
	root, ok, reason := topology.RootForSession(sessionName)
	if !ok {
		t.Fatalf("RootForSession(%q) = false/%s", sessionName, reason)
	}
	return root.RootID
}
