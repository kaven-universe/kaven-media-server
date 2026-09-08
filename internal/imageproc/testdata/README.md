# Legacy animation contract

`legacy-animation.json` contains a deterministic two-frame GIF and metadata
captured with Sharp 0.35.4 from the read-only legacy backend installation.
The source was created in memory as two 2x2 RGB pages with 100/200 ms delays
and Sharp loop value 3 (the GIF repeat count is 2). It was transformed with the same calls as the legacy
controller:

```js
sharp(source, { failOn: "none" }).resize({ width: 1 }).toBuffer()
```

Input and output metadata were read with `sharp(buffer, { animated: true })`.
The legacy transform returns a 1x1, one-frame GIF because its controller does
not enable animated input. The base64 fixture is decoded and structurally
validated with Go's standard GIF decoder even in non-libvips builds. The
production libvips matrix asserts the matching first-frame behavior against
the final container libraries on AMD64 and ARM64.
