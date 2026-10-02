# Response interception and replacement rules

The proxy can pause requests and responses independently. Start with a narrow
Target scope, then enable the relevant switch in Intercept. Request rules match
the outgoing request; response rules also support response MIME type and status.
Automatic replacement rules run before the matching message enters the manual
queue. Turning off manual interception does not disable replacement rules.

## Editing a response

Enable response interception and browse an in-scope URL through the proxy. The
response queue shows the result after automatic replacements. Change its status,
headers or editable body and choose Forward. Drop returns a local 502 response
to the browser and records the reason in History. Dropping a response cannot undo
the request already received by the upstream server.

Request and response interception each have their own switch and filters. Saving
one group preserves the other settings. Invalid edits leave the message pending
so they can be corrected. Pending messages expire; keep the operator UI open
while pausing traffic.

## Ordered replacements

Add a rule, choose request or response, then select URL (requests only), header
value or body. Header rules address an existing named header; they do not add a
missing header. Enable regular expressions for capture-based replacements, or
leave them disabled for literal search and replacement. Rules execute in their
displayed order, so a later rule sees earlier changes. Move a rule up or down to
change its position.

For example, a response body rule with pattern `server-([0-9]+)` and replacement
`preview-${1}` changes `server-42` to `preview-42`. Use `$$` for a literal dollar
sign in regex replacements. Literal replacements treat dollar signs literally.
The regex syntax is Go's standard syntax; lookaround and backreferences in the
pattern are not supported. Invalid expressions are rejected on save.
Each rule permits at most 64 capture groups and 10,000 matches per message field;
replacement output is also size-limited. Exceeding a limit returns a local proxy
error rather than silently applying part of the rule. Each rule list has at most
100 entries, and each interception queue holds at most 100 pending messages.

Host, path and MIME filters restrict each replacement rule further. All
replacements require the request to be in scope. A URL edit that leaves scope is
rejected before forwarding. Framing, connection and content-encoding headers are
protected so that the transmitted headers continue to describe the actual body.

## Body limits and history

Body editing and replacement are limited to complete, text-safe bodies within
the configured capture limit (`BC_BODY_LIMIT_BYTES`, default 1 MiB). Binary,
compressed and streaming bodies are not decoded for editing in v2b. Oversized
bodies and bodies with unknown length (including chunked responses) bypass body
editing and replacement. Their headers can still be reviewed and edited.
Oversized bodies continue through the streaming path without truncating what the client
receives. The History preview can still be truncated by its capture limit.

History records the final request and response, whether the response was
intercepted, and IDs of replacement rules that changed content. It does not keep
full original copies or historical rule definitions. The IDs identify the rules
used at the time; later editing or deleting a rule does not alter the recorded
message. Existing request-only settings and older projects remain supported.

These controls apply to traffic through the proxy. Repeater uses its own send
path and does not automatically apply proxy replacement rules.
