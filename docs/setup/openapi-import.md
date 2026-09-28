# Local OpenAPI import

Open **API Import**, paste an OpenAPI 3.0/3.1 JSON document or choose a `.json`
file, then select **Import specification**. The app lists GET, POST, PUT,
PATCH, and DELETE operations. Enter or correct an absolute HTTP(S) base URL,
filter the list, and send one operation to Repeater. Path templates such as
`{id}` become visible `__id__` placeholders. Replace them and add any needed
headers, query parameters, or body before sending.

Import is browser-local and sends no requests. The document stays in memory
while navigating between API Import and Repeater, but is not saved to SQLite
or uploaded. Limits: 1 MiB JSON, 500 paths, 2,000 supported operations, and
100 visible results at a time. Invalid paths and unsafe base URLs are rejected.

This is an endpoint inventory, not full OpenAPI execution. It does not resolve
external references, generate schema-driven bodies or authentication, import
YAML, or support GraphQL/SOAP. Repeater's normal scope and request safeguards
still apply when the operator explicitly sends a draft.
