# Local Workbench

Open **Workbench** in the navigation for three offline tools:

- **Decoder:** URL, Base64, UTF-8 hex, and HTML entity encoding/decoding. It
  accepts UTF-8 text, not arbitrary binary files. Malformed Base64, hex, URL
  encoding, or UTF-8 is rejected rather than silently replaced. Input is
  limited to 1 MiB.
- **Comparer:** line-by-line additions and removals, with a 64 KiB and
  500-line limit per side. It is not a binary or semantic diff.
- **Token analysis:** length, distinct characters, and observed Shannon
  entropy per character for one sample. This does not measure generator
  quality, predictability across samples, or cryptographic strength.
- **JWT inspector:** decodes the header and claims of a three-part compact JWT
  with a 64 KiB input limit. It does not verify the signature, expiry, issuer,
  audience, or any other security property. Treat all displayed claims as
  untrusted input.

These inputs remain in the current browser tab's memory. They are not sent to
the Go backend, saved in the project database, or synchronized elsewhere.
Leaving the Workbench unmounts the tool and clears its inputs. Avoid pasting
live secrets into an untrusted browser environment.

**History** returns to captured traffic. **Repeater** opens the existing
request editor as a focused workspace. **Extensions** is disabled because an
extension API is not yet implemented.
