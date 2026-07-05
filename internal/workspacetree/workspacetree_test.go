package workspacetree

import "testing"

func TestNearestParentDoesNotRequireRegisteredRoot(t *testing.T) {
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo"},
		{SessionName: "project", Label: "api", ParentSessionName: "repo"},
	})

	parent, ok, reason := topology.NearestParent("project")
	if !ok {
		t.Fatalf("NearestParent = false/%s, want repo parent", reason)
	}
	if parent.SessionName != "repo" {
		t.Fatalf("parent session = %q, want repo", parent.SessionName)
	}
}

func TestNearestChildrenUsesExplicitHierarchyAndOrder(t *testing.T) {
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo"},
		{SessionName: "api", Label: "api", ParentSessionName: "repo", Order: 20},
		{SessionName: "pkg", Label: "pkg", ParentSessionName: "api"},
		{SessionName: "docs", Label: "docs", ParentSessionName: "repo", Order: 10},
	})

	children, reason := topology.NearestChildren("repo")
	if reason != FailureNone {
		t.Fatalf("NearestChildren reason = %s, want none", reason)
	}
	got := []string{}
	for _, child := range children {
		got = append(got, child.SessionName)
	}
	want := []string{"docs", "api"}
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

func TestExplicitHierarchyOverridesRootAncestryMetadata(t *testing.T) {
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo", Root: "/workspace/repo"},
		{SessionName: "other", Label: "other", Root: "/workspace/other"},
		{SessionName: "api", Label: "api", ParentSessionName: "other", Root: "/workspace/repo/apps/api"},
	})

	parent, ok, reason := topology.NearestParent("api")
	if !ok {
		t.Fatalf("NearestParent = false/%s, want explicit parent", reason)
	}
	if parent.SessionName != "other" {
		t.Fatalf("parent session = %q, want explicit parent other", parent.SessionName)
	}
}

func TestDuplicateHierarchySessionsAreReportedAndAmbiguous(t *testing.T) {
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo-a", ID: "repo-a"},
		{SessionName: "repo", Label: "repo-b", ID: "repo-b"},
		{SessionName: "project", Label: "api", ParentSessionName: "repo"},
	})

	diagnostics := topology.Diagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one duplicate_session", diagnostics)
	}
	if diagnostics[0].Code != "duplicate_session" {
		t.Fatalf("diagnostic code = %q, want duplicate_session", diagnostics[0].Code)
	}
	if len(diagnostics[0].IDs) != 2 {
		t.Fatalf("diagnostic ids = %#v, want both duplicate ids", diagnostics[0].IDs)
	}

	_, ok, reason := topology.NearestParent("project")
	if ok || reason != FailureAmbiguousHierarchy {
		t.Fatalf("NearestParent duplicate = ok:%v reason:%s, want ambiguous_hierarchy", ok, reason)
	}
	resolution := topology.ResolveAlias("@parent/worker", "project", nil)
	if resolution.Found || resolution.FailureReason != FailureAmbiguousHierarchy {
		t.Fatalf("ResolveAlias duplicate = %#v, want ambiguous_hierarchy", resolution)
	}
}

func TestResolveAliasCompilesToExplicitSessionNodeAddress(t *testing.T) {
	topology := Build([]Registration{
		{SessionName: "repo", ID: "repo-id", Label: "repo", Representative: "orchestrator"},
		{SessionName: "api", ID: "api-id", Label: "api", ParentSessionName: "repo", Representative: "worker"},
		{SessionName: "docs", ID: "docs-id", Label: "docs", ParentSessionName: "repo", Representative: "worker"},
	})

	defaultParent := topology.ResolveAlias("@parent", "api", nil)
	if !defaultParent.Found || defaultParent.Address != "repo:orchestrator" {
		t.Fatalf("default parent alias = %#v, want repo:orchestrator", defaultParent)
	}

	parent := topology.ResolveAlias("@parent/orchestrator", "api", nil)
	if !parent.Found || parent.Address != "repo:orchestrator" {
		t.Fatalf("parent alias = %#v, want repo:orchestrator", parent)
	}

	child := topology.ResolveAlias("@child/api/worker", "repo", nil)
	if !child.Found || child.Address != "api:worker" {
		t.Fatalf("child alias = %#v, want api:worker", child)
	}

	byID := topology.ResolveAlias("@child/docs-id/worker", "repo", nil)
	if !byID.Found || byID.Address != "docs:worker" {
		t.Fatalf("child alias by id = %#v, want docs:worker", byID)
	}

	defaultChild := topology.ResolveAlias("@child/api", "repo", nil)
	if !defaultChild.Found || defaultChild.Address != "api:worker" {
		t.Fatalf("default child alias = %#v, want api:worker", defaultChild)
	}
}

func TestRelationshipAliasNamesRepresentativeContacts(t *testing.T) {
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo", Representative: "orchestrator"},
		{SessionName: "api", Label: "api", ParentSessionName: "repo", Representative: "worker"},
	})

	parent, ok := topology.RelationshipAlias("api:worker", "repo:orchestrator")
	if !ok || parent != "@parent" {
		t.Fatalf("parent contact alias = %q/%v, want @parent", parent, ok)
	}
	child, ok := topology.RelationshipAlias("repo:orchestrator", "api:worker")
	if !ok || child != "@child/api" {
		t.Fatalf("child contact alias = %q/%v, want @child/api", child, ok)
	}
	if alias, ok := topology.RelationshipAlias("repo:orchestrator", "api:reviewer"); ok {
		t.Fatalf("non-representative child alias = %q/%v, want none", alias, ok)
	}
}

func TestResolveAliasFailuresAreTyped(t *testing.T) {
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo"},
		{SessionName: "api", Label: "api", ParentSessionName: "repo"},
		{SessionName: "docs", Label: "docs", ParentSessionName: "repo"},
		{SessionName: "orphan", Label: "orphan", ParentSessionName: "missing-parent"},
	})

	cases := []struct {
		name    string
		alias   string
		session string
		want    FailureReason
	}{
		{name: "invalid", alias: "@child/api/worker/extra", session: "repo", want: FailureInvalidAlias},
		{name: "unknown source", alias: "@parent/worker", session: "missing", want: FailureUnknownSourceSession},
		{name: "ambiguous child", alias: "@child/worker", session: "repo", want: FailureAmbiguousChild},
		{name: "no parent", alias: "@parent/worker", session: "repo", want: FailureNoParent},
		{name: "unknown parent", alias: "@parent/worker", session: "orphan", want: FailureUnknownParent},
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
	topology := Build([]Registration{
		{SessionName: "repo", Label: "repo"},
		{SessionName: "api", Label: "api", ParentSessionName: "repo"},
	})

	resolution := topology.ResolveAlias("@parent/orchestrator", "api", func(address string) bool {
		return address == "repo:messenger"
	})
	if resolution.Found || resolution.FailureReason != FailureUnknownNode || resolution.Address != "repo:orchestrator" {
		t.Fatalf("ResolveAlias nodeExists = %#v, want unknown_node with compiled address", resolution)
	}
}
