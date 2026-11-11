package xcode

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Testables returns the test target names a scheme runs, resolving test plans when
// the scheme uses them. containers are the .xcworkspace / .xcodeproj bundles the
// scheme may live in. Returns nil, nil when the scheme is not found.
//
// A non-empty testPlan keeps only that plan's targets: xcodebuild runs one plan, so a
// target of another plan must not be handed to its -only-testing.
func Testables(scheme, testPlan string, containers []string) ([]string, error) {
	schemePath, container := findScheme(scheme, containers)
	if schemePath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(schemePath)
	if err != nil {
		return nil, err
	}
	var doc struct {
		TestAction struct {
			Testables struct {
				Refs []struct {
					Skipped   string `xml:"skipped,attr"`
					Buildable struct {
						BlueprintName string `xml:"BlueprintName,attr"`
					} `xml:"BuildableReference"`
				} `xml:"TestableReference"`
			} `xml:"Testables"`
			TestPlans struct {
				Refs []struct {
					Reference string `xml:"reference,attr"`
					Default   string `xml:"default,attr"`
				} `xml:"TestPlanReference"`
			} `xml:"TestPlans"`
		} `xml:"TestAction"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse scheme %s: %w", schemePath, err)
	}

	if testPlan != "" && len(doc.TestAction.TestPlans.Refs) == 0 {
		return nil, fmt.Errorf("test plan %s requested, but scheme %s uses no test plans", testPlan, scheme)
	}

	var names []string
	for _, r := range doc.TestAction.Testables.Refs {
		if strings.EqualFold(r.Skipped, "YES") {
			continue
		}
		if r.Buildable.BlueprintName != "" {
			names = append(names, r.Buildable.BlueprintName)
		}
	}

	// Test plans replace the testables list; without a requested plan every referenced
	// plan may be run, so the candidates are the union of their targets.
	planFound := false
	for _, ref := range doc.TestAction.TestPlans.Refs {
		planPath := resolveLocation(ref.Reference, filepath.Dir(container), filepath.Dir(container))
		if planPath == "" {
			continue
		}
		if testPlan != "" && strings.TrimSuffix(filepath.Base(planPath), ".xctestplan") != testPlan {
			continue
		}
		planFound = true
		planTargets, err := testPlanTargets(planPath)
		if err != nil {
			return nil, err
		}
		names = append(names, planTargets...)
	}
	if testPlan != "" && !planFound {
		return nil, fmt.Errorf("test plan %s is not referenced by scheme %s", testPlan, scheme)
	}
	return dedupe(names), nil
}

func testPlanTargets(planPath string) ([]string, error) {
	data, err := os.ReadFile(planPath)
	if err != nil {
		return nil, fmt.Errorf("read test plan %s: %w", planPath, err)
	}
	var plan struct {
		TestTargets []struct {
			Target struct {
				Name string `json:"name"`
			} `json:"target"`
		} `json:"testTargets"`
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("parse test plan %s: %w", planPath, err)
	}
	var out []string
	for _, t := range plan.TestTargets {
		if t.Target.Name != "" {
			out = append(out, t.Target.Name)
		}
	}
	return out, nil
}

// findScheme looks for <scheme>.xcscheme under the shared and per-user scheme
// directories of every container.
func findScheme(scheme string, containers []string) (string, string) {
	name := scheme + ".xcscheme"
	for _, c := range containers {
		candidates := []string{filepath.Join(c, "xcshareddata", "xcschemes", name)}
		users, _ := filepath.Glob(filepath.Join(c, "xcuserdata", "*", "xcschemes", name))
		candidates = append(candidates, users...)
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p, c
			}
		}
	}
	return "", ""
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
