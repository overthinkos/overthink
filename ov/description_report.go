package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// FormatScenarioResultsText emits a human-readable report of
// scenario-level outcomes to w. Each scenario shows:
//
//	Scenario: <name>  <PASS | FAIL>  (attempts over elapsed when non-trivial)
//	  <KEYWORD> <text>  <status>  <message>
//
// OnFail steps are rendered under a "on_fail:" sub-heading when the
// scenario failed so the diagnostic output they produce is visible.
func FormatScenarioResultsText(w io.Writer, results []ScenarioResult) {
	var passed, failed, skipped int
	for _, sr := range results {
		statusBadge := "PASS"
		if sr.Status == TestFail {
			statusBadge = "FAIL"
			failed++
		} else {
			passed++
		}

		fmt.Fprintf(w, "\nScenario: %s  %s\n", sr.Name, statusBadge)
		if sr.Origin != "" {
			fmt.Fprintf(w, "  origin: %s\n", sr.Origin)
		}
		for _, step := range sr.Steps {
			renderStep(w, &step)
			if step.Result.Status == TestSkip {
				skipped++
			}
		}
		if len(sr.OnFail) > 0 {
			fmt.Fprintln(w, "  on_fail:")
			for _, step := range sr.OnFail {
				renderStep(w, &step)
			}
		}
	}
	fmt.Fprintf(w, "\n%d scenario%s: %d passed, %d failed, %d step%s skipped\n",
		len(results), plural(len(results)), passed, failed, skipped, plural(skipped))
}

func renderStep(w io.Writer, step *StepResult) {
	status := strings.ToUpper(step.Result.Status.String())
	retryInfo := ""
	if step.Result.Attempts > 1 {
		retryInfo = fmt.Sprintf(" (attempts=%d, elapsed=%s)",
			step.Result.Attempts, step.Result.TotalElapsed.Round(time.Millisecond))
	}
	msg := step.Result.Message
	if msg != "" {
		msg = "  " + msg
	}
	fmt.Fprintf(w, "  %-5s %s %s  [%s]%s%s\n",
		status, step.Keyword, step.Text, step.StepID, retryInfo, truncate(msg, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FormatScenarioResultsJSON emits a structured JSON document.
func FormatScenarioResultsJSON(w io.Writer, results []ScenarioResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}

// FormatScenarioResultsTAP emits TAP v13. Each scenario is one TAP
// test point; failing scenarios include a YAML block with step-level
// detail.
func FormatScenarioResultsTAP(w io.Writer, results []ScenarioResult) {
	fmt.Fprintln(w, "TAP version 13")
	fmt.Fprintf(w, "1..%d\n", len(results))
	for i, sr := range results {
		directive := "ok"
		if sr.Status == TestFail {
			directive = "not ok"
		}
		fmt.Fprintf(w, "%s %d - %s\n", directive, i+1, sr.Name)
		if sr.Status == TestFail {
			fmt.Fprintln(w, "  ---")
			fmt.Fprintf(w, "  origin: %q\n", sr.Origin)
			fmt.Fprintf(w, "  scenario_id: %q\n", sr.ScenarioID)
			fmt.Fprintln(w, "  steps:")
			for _, step := range sr.Steps {
				fmt.Fprintf(w, "    - %s %s: %s (%s)\n",
					step.Keyword, step.Text,
					strings.ToUpper(step.Result.Status.String()),
					step.Result.Message)
			}
			fmt.Fprintln(w, "  ...")
		}
	}
}

// FormatScenarioResultsJUnit emits JUnit XML for CI dashboards.
// Scenarios surface as <testsuite>, steps as <testcase>.
func FormatScenarioResultsJUnit(w io.Writer, results []ScenarioResult) error {
	type junitFailure struct {
		Message string `xml:"message,attr"`
		Body    string `xml:",chardata"`
	}
	type junitSkipped struct {
		Message string `xml:"message,attr"`
	}
	type junitTestCase struct {
		XMLName   xml.Name      `xml:"testcase"`
		Name      string        `xml:"name,attr"`
		Classname string        `xml:"classname,attr"`
		Time      float64       `xml:"time,attr"`
		Failure   *junitFailure `xml:"failure,omitempty"`
		Skipped   *junitSkipped `xml:"skipped,omitempty"`
	}
	type junitTestSuite struct {
		XMLName  xml.Name        `xml:"testsuite"`
		Name     string          `xml:"name,attr"`
		Tests    int             `xml:"tests,attr"`
		Failures int             `xml:"failures,attr"`
		Skipped  int             `xml:"skipped,attr"`
		Time     float64         `xml:"time,attr"`
		Cases    []junitTestCase `xml:"testcase"`
	}
	type junitTestSuites struct {
		XMLName xml.Name         `xml:"testsuites"`
		Suites  []junitTestSuite `xml:"testsuite"`
	}

	var suites junitTestSuites
	for _, sr := range results {
		suite := junitTestSuite{Name: sr.Name}
		var totalTime float64
		for _, step := range sr.Steps {
			elapsed := step.Result.Elapsed.Seconds()
			if step.Result.TotalElapsed > 0 {
				elapsed = step.Result.TotalElapsed.Seconds()
			}
			totalTime += elapsed
			tc := junitTestCase{
				Name:      step.Keyword + " " + step.Text,
				Classname: sr.Origin,
				Time:      elapsed,
			}
			switch step.Result.Status {
			case TestFail:
				tc.Failure = &junitFailure{
					Message: step.Result.Message,
					Body:    "Verb: " + step.Result.Verb + "\nStep ID: " + step.StepID,
				}
				suite.Failures++
			case TestSkip:
				tc.Skipped = &junitSkipped{Message: step.Result.Message}
				suite.Skipped++
			}
			suite.Cases = append(suite.Cases, tc)
		}
		suite.Tests = len(suite.Cases)
		suite.Time = totalTime
		suites.Suites = append(suites.Suites, suite)
	}

	fmt.Fprintln(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(suites); err != nil {
		return err
	}
	fmt.Fprintln(w)
	return nil
}
