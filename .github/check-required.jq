# SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
# SPDX-License-Identifier: MIT OR Apache-2.0

include "required";

def modes:
  [false, "false", "false", ["skipped", "skipped", "skipped", "skipped"]],
  [false, "false", "true", ["skipped", "skipped", "skipped", "success"]],
  [false, "true", "false", ["success", "success", "success", "skipped"]],
  [false, "true", "true", ["success", "success", "success", "success"]],
  [true, "false", "false", ["success", "success", "success", "success"]],
  [true, "false", "true", ["success", "success", "success", "success"]],
  [true, "true", "false", ["success", "success", "success", "success"]],
  [true, "true", "true", ["success", "success", "success", "success"]];

def result: "success", "failure", "cancelled", "skipped";

def cases:
  modes as $mode |
  result as $changes | result as $windows | result as $race |
  result as $policy | result as $release |
  {
    full: $mode[0],
    needs: {
      changes: {result: $changes, outputs: {runtime: $mode[1], release: $mode[2]}},
      windows: {result: $windows}, race: {result: $race},
      policy: {result: $policy}, "release-check": {result: $release}
    },
    expected: ($changes == "success" and [$windows, $race, $policy, $release] == $mode[3])
  };

def report(verdict):
  reduce cases as $case ({total: 0, matched: 0, admitted: 0};
    ($case | verdict) as $actual |
    .total += 1 |
    .matched += (if $actual == $case.expected then 1 else 0 end) |
    .admitted += (if $actual == true then 1 else 0 end));

def actual: . as $case | .needs | required($case.full);

def invalid:
  null, 0, "invalid", [],
  (.full = null), (.full = "false"), (.full = 1),
  (del(.needs.changes.outputs)),
  (["changes", "windows", "race", "policy", "release-check"][] as $job |
    del(.needs[$job]),
    (.needs[$job] = null),
    (.needs[$job].result = null),
    (.needs[$job].result = "unknown")),
  (["runtime", "release"][] as $output |
    del(.needs.changes.outputs[$output]),
    ([null, true, false, 0, 1, "", "TRUE", [], {}][] as $value |
      .needs.changes.outputs[$output] = $value));

report(actual) as $production |
report(true) as $approve_all |
report(false) as $reject_all |
report(.needs | all(.[]; .result | IN("success", "skipped"))) as $allow_skips |
{
  full: false,
  needs: {
    changes: {result: "success", outputs: {runtime: "true", release: "true"}},
    windows: {result: "success"}, race: {result: "success"},
    policy: {result: "success"}, "release-check": {result: "success"}
  }
} |
[invalid | (try actual catch false) == false] as $invalid |
if $production == {total: 8192, matched: 8192, admitted: 8} and
   $approve_all.matched < $production.total and
   $reject_all.matched < $production.total and
   $allow_skips.matched < $production.total and
   ($invalid | length) == 48 and ($invalid | all)
then {cases: $production.total, invalid: ($invalid | length), rejecting_controls: 3}
else error("required-job contract failed")
end
