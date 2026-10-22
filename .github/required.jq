# SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
# SPDX-License-Identifier: MIT OR Apache-2.0

def required($full):
  . as $needs |
  ($full | type) == "boolean" and
  $needs.changes.result == "success" and
  ([$needs.changes.outputs.runtime, $needs.changes.outputs.release] |
    all(IN("true", "false"))) and
  ([
    {name: "windows", selected: ($full or $needs.changes.outputs.runtime == "true")},
    {name: "release-check", selected: ($full or $needs.changes.outputs.release == "true")}
  ] | all(.[]; . as $job |
    $needs[$job.name].result == (if $job.selected then "success" else "skipped" end)));
