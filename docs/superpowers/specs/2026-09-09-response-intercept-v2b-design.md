# Response Intercept and Match-and-Replace v2b

## Approved behavior

Extend the existing local Go proxy and React UI with independently configured
request and response interception. Apply ordered literal or Go regular expression
replacement rules before manual interception, exclusively to in-scope traffic.
After URL changes, recheck scope before forwarding. Dropping a response returns a
local 502 response and records the reason. History stores final transmitted
messages and applied rule IDs, without complete before-images.

## Processing

Use shared request/response preparation helpers for HTTP and HTTPS. Preserve the
streaming path when no body processing is required. Buffer at most the configured
body limit plus one byte. Oversized, encoded, binary and streaming bodies bypass
body editing/replacement without losing bytes. Clearly describe this limitation
in the UI and documentation. Headers remain editable where protocol-safe.
Normalize framing after edits, respect HEAD and bodyless status codes, reject
invalid status codes, header injection and invalid URLs. Never mutate content
encoding metadata in a way that misrepresents untouched bytes.

The request and response queues are separate but use a shared queue implementation.
Add finite queue capacity and timeout; ensure cancellation releases resources.
Do not reapply replacements after manual editing. Validate all replacement rules
before activating configuration, and persist configuration before activation.
Reject invalid regex, duplicate IDs, excessive rule count/pattern size and
unbounded replacement expansion. Existing saved request-only settings still load.

## Integration contract

ControllerState preserves enabled and rules, and adds responseEnabled,
responseRules and replacementRules. ReplacementRule has id, enabled, direction
(request|response), target (url|header|body), header, pattern, replacement, regex,
hostContains, pathContains and mimeContains. Response URL rules are invalid.
Intercept Rule adds optional statusCode (0 means any). New response rules default
to one enabled match-all rule, while response interception starts disabled.

Controller exposes ResponseQueue(), ValidateState(ControllerState) error as a
package function and Update(ControllerState) with existing signature. Response
queue DTOs add phase and statusCode; request DTOs may omit phase for compatibility.
Use GET /api/intercept/response-queue and POST
/api/intercept/response/{id}/forward or /drop, alongside existing config endpoints.
Forward response DTO uses statusCode, headers and body. Exchanges add
AppliedRuleIDs []string and ResponseIntercepted bool, persisted and exposed as
appliedRuleIds and responseIntercepted in detail responses.

## Verification

Exercise actual HTTP and HTTPS traffic: independent switches, regex captures,
ordered replacements before pause, URL scope escape rejection, final history,
response drop, HEAD/bodyless responses, binary/encoded/oversized preservation,
queue cancellation/capacity, invalid input and persistence failures. Run Go tests,
race tests, vet, frontend tests/build and Windows compilation checks. Review the
integrated change before committing. Keep the existing PR in draft.
