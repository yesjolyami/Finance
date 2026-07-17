# NomadHost deployment adapter

This adapter runs the verified Linux release inside a single NomadHost Ubuntu
container. Nginx binds the public allocation, rejects unexpected `Host` values,
serves the React build, applies the security headers and rate limits, and proxies
only `/api/` to the Go API on `127.0.0.1:8080`. NomadHost's subdomain proxy is
responsible for public TLS.

Requirements:

- Ubuntu amd64 container with Bash, Python 3, Nginx and CA certificates;
- exactly one NomadHost allocation, exposed only through an HTTPS subdomain;
- `SERVER_PORT` supplied by the NomadHost startup environment;
- external PostgreSQL DSN stored in `secrets/database-url` as a regular owner-only
  file with mode `0400` or `0600`;
- release frontend already built for the selected Supabase project.

Build a bundle without secrets:

```bash
deploy/nomadhost/build-bundle.sh \
  /path/to/finance-release.tar.gz \
  /tmp/finance-nomadhost \
  moneta.nhost.pp.ua \
  rzbjrqisloscwkyweilv.supabase.co
```

Upload the generated archive into `/opt/finance/current` and extract it there.
Copy the contents of `nomadhost-root-launcher.sh` into the root `/run.sh`, then
create `/opt/finance/current/secrets/database-url` in the panel without adding it
to any archive. The panel startup command remains `./run.sh`. Keep
`HSTS_MAX_AGE=0` until HTTPS, auth, backup and rollback checks have passed.

The build script disables macOS AppleDouble metadata and excludes `._*` files
from the upload archive. The root launcher also removes such files from the
migration directory before startup to recover safely from an archive created by
Finder or the default macOS archiver.

The public allocation must not be advertised or used directly. The default
Nginx virtual server returns an empty `444` response for any host other than the
configured domain.
