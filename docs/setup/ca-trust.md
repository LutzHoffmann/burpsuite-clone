# Trusting the Local CA

The proxy generates a local certificate authority (CA) for HTTPS interception. Use it only for systems you own or are authorized to test. Download the CA certificate from Settings; never share or export its private key.

## macOS Keychain

Open Keychain Access and import the downloaded CA certificate into the login keychain. Open the imported certificate, expand Trust, and set it to trust for SSL. Restart browsers that were already running.

## Windows Certificate Manager

Run `certmgr.msc`, import the downloaded CA into Trusted Root Certification Authorities for the current user, and confirm the trust prompt. Restart the browser after importing it.

## Linux NSS and system store

For browsers using NSS, import the certificate into the browser profile's NSS database with your distribution's `certutil` package. For system-trust browsers, copy the certificate to the distribution's local CA directory and run its CA-store update command, such as `update-ca-certificates` or `update-ca-trust`. Consult your distribution documentation for the exact directory and command.

## Firefox Certificate Manager

Open Settings, search for Certificates, select View Certificates, then Authorities. Import the downloaded CA and allow it to identify websites. Firefox may use its own certificate store even when the system store is configured.

## Chrome

Chrome uses the operating system trust store on macOS and Windows. On Linux, behavior depends on the distribution and browser packaging; use the system store or NSS instructions above, then restart Chrome.

## Test Device Proxy

On a test device, set the Wi-Fi or network proxy host to the machine running the proxy and set the port to `8080`. The proxy must listen on an address reachable from the device. Import the downloaded CA on the test device using that operating system's certificate settings. Use this only on test devices and authorized networks.

## Remove Trust When Finished

Remove the local CA certificate from every browser, system trust store, and test device when testing is complete. Deleting trust does not remove proxy project data; remove local project data separately if it is no longer needed.
