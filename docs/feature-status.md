# Burp-like feature status

This project is an independent local testing suite, not a complete or
compatible replacement for Burp Suite Professional. This inventory tracks
the original requested categories without treating partial tools as full
parity.

| Area | Current state | Important gap |
| --- | --- | --- |
| HTTP/HTTPS proxy, interception, replacements | Implemented for local manual work | No integrated dedicated browser; protocol coverage is not Burp parity |
| WebSockets | Forwarding, history, and manual replay | Not a full message-interception or extension platform |
| Target scope, Site Map, History | Implemented with local SQLite storage | No team workspace or cloud sync |
| Repeater and CSRF PoC | Up to 20 independent in-memory request tabs with separate drafts, send state and responses; manual replay and GET/form-POST PoC generation | Tabs are not persisted across reloads; no request sequences or macros; PoC does not prove exploitability |
| Intruder | Bounded, persistent fuzzing with four attack modes | Not Turbo Intruder-scale throughput |
| Crawler | Bounded same-origin GET crawl | No JavaScript rendering, automatic login, or form submission |
| Passive findings | HSTS, CSP, nosniff, and selected HTTPS cookie-attribute observations | Not a comprehensive passive scanner; missing attributes are not confirmed vulnerabilities |
| Active scanner | Bounded GET reflection plus opt-in open-redirect and credentialed-CORS observations | No broad vulnerability coverage or confirmed findings |
| Authenticated testing | Explicit one-run Cookie/Authorization headers for crawl and active checks | No login automation, refresh, or session-expiry handling |
| API testing | Local OpenAPI 3.0/3.1 JSON inventory with parameter/example and simple required-field Repeater drafts | No YAML, GraphQL, SOAP, full schema execution, or API scanning |
| Decoder, Comparer, token/JWT inspection | Browser-local text utilities | No generator-quality analysis across samples or JWT signature verification |
| Reports | HTML export of active-check observations | No unified findings report, PDF, or XML export |
| OAST, DOM testing, custom checks | Not implemented | Requires separate infrastructure and substantial scanner work |
| Extensions, collaboration | Not implemented | No BApp/Montoya compatibility, plugin API, or shared project server |

The app should be used only for authorized testing. A green CI run verifies
the implemented subset, not Burp feature parity or the absence of security
bugs.
