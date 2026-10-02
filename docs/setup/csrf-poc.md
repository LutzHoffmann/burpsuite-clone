# Manual CSRF PoC

In Repeater, edit a GET or `application/x-www-form-urlencoded` POST request
and select **Generate CSRF PoC**. Review the generated HTML in the read-only
field or download it. The generated page contains a normal HTML form and a
manual submit button; it never submits itself.

The generator preserves repeated form fields and HTML-escapes the target URL,
field names, and values. It accepts at most a 4 KiB URL, a 64 KiB body, and
100 fields. It rejects unsupported methods, non-HTTP targets, URL credentials,
fragments, malformed percent encoding, and POST bodies that are not
form-urlencoded. Editing the Repeater draft clears the prior result.

A browser form cannot reproduce custom request headers, explicit
Authorization headers, multipart uploads, JSON bodies, or other non-form
requests. Browser cookies, SameSite rules, CORS, and application CSRF defenses
still determine whether a real cross-site request can succeed. The generated
form is a testing aid, not proof of a vulnerability. Use it only on systems you
are authorized to assess.
