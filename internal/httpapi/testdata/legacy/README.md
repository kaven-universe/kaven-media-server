# Legacy compatibility fixtures

These fixtures describe observable contracts in Kaven Image Server revision
`81de1bf92f9e76079203bd0a4e0175f404a4f9c4` and frontend expectations in Kaven
Image revision `74fe40c6ae9daaea996d5f45ed524b7f31dadc91`.

The corpus was captured from the route handlers, Mongoose schemas, shared
TypeScript interfaces, and frontend consumers because the legacy service needs
an external MongoDB instance and a site-specific configuration to start. Values
that vary per installation, such as IDs, timestamps, paths, limits, and digest
nonces, are replaced with stable representative values. The status codes, JSON
field names, response cardinality, and omitted optional fields follow the legacy
implementation.

`contracts.json` maps requests to response bodies. `assets.json` records the
checksums and media types of representative files. Tests validate that every
referenced fixture exists, that JSON bodies remain syntactically valid, and
that file bytes do not change unnoticed.

When a configured legacy instance becomes available, replay these cases against
it and record any observed difference before implementing the corresponding
handler.
