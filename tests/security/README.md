# Native security E2E

These tests require an isolated `scripts/native-fixture.py` output and the native
FrankenPHP runtime. They use real Unix sockets and kernel peer checks. The two
business identities are synthetic resolver inputs running under the current OS
account; they do not establish real NAS identity or OS-user isolation.

```sh
python3 scripts/native-fixture.py --source .build/frontend-check/2FAuth
FN2AUTH_SECURITY_FIXTURE=/absolute/path/printed/by/native-fixture \
FN2AUTH_SECURITY_EVIDENCE="$PWD/.build/security/native" \
  nice -n 10 taskset -c 0-7 /path/to/go test \
  -modfile=.build/bootstrap/adapter.mod ./tests/security \
  -run TestNativeBoundaries -count=1 -v -timeout 8m
```

The fixture must have `fixture.json`, `config.json`, and its own copied application.
Each run creates fresh private state beneath that fixture. A temporary PHP probe
is appended only to the copied request adapter, after its real private-channel
authentication, and restored at test completion. Do not run concurrently against
the same fixture. The probe returns no instance secret or application key.

The suite checks untrusted Unix peers, API origin requirements and `index.php`
aliases, PHP header normalization, two private API identities, per-user storage,
cookie forwarding and cookie attributes, distinct application keys, and refusal
to retry an interrupted migration. Evidence includes `security-checks.json` under
the printed run directory and `checks.json` under the explicit evidence directory.
Existing state and evidence are retained; no application installation is performed.
