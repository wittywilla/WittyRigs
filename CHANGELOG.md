# Changelog

What changed in WittyRigs, newest first. What changed before it had a repository of its own is in the
[WittyMediaServer changelog](https://github.com/wittywilla/WittyMediaServer/blob/master/CHANGELOG.md).

## Unreleased

- Finds Sunshine where its installers put it on Windows, Linux (the package and the Flatpak), FreeBSD and macOS (Homebrew too). The server starts this helper with no PATH or ProgramFiles, so before, it only ever found the macOS app.
- Fits servers up to 0.8.x, so WittyMediaServer 0.8.0-alpha accepts it; it was limited to 0.6.x.
- A helper, `wittyrigs-module`, is now how a server reaches WittyRigs, with no copy compiled in.
- WittyRigs is now a project of its own, developed and released from this repository.
