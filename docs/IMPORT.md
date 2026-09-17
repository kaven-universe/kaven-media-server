# Database import

Kaven Media Server does not detect or migrate another application's schema or
filesystem layout at runtime. A separate importer must produce a
`kaven-media.db` that matches the current schema. Only that database is imported
into the application data directory.

Media and HFS files are not copied as part of database import. Stop the server,
place the converted database in a clean data directory, and start the matching
Kaven Media Server version. Then use Settings to configure:

- the upload directory, relative to the application data directory;
- the download directory, relative to the application data directory;
- every HFS virtual root as either a data-directory-relative path or an absolute
  server path;
- the public and read-only policy for each HFS root.

Copy or mount each physical file tree at the configured location before running
the storage integrity check. Database media references must already be
canonical paths relative to their configured roots. Filenames must match the
database exactly. The server does not rewrite paths, probe historical directory
names, or search for renamed files.

Keep the source database and file trees unchanged until representative image,
Bing, and HFS records have been verified in the new installation.
