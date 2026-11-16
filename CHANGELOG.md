# Changelog

What changed in WittyRigs, newest first. What changed before it had a repository of its own is in the
[WittyMediaServer changelog](https://github.com/wittywilla/WittyMediaServer/blob/master/CHANGELOG.md).

## 0.4.1

- The README's pictures are retaken from WittyMediaServer 0.8.0-alpha.

## 0.4.0

- Built on WittyModule 0.5.4 and module API 5; its workflows use WittyModule 0.5.4.
- **Module API 5: WittyRigs owns its whole feature.** Game rigs (gaming PCs shared as games libraries: adding, pairing, waking, their games, starting one on the PC, a PIN for another device), this server's own Sunshine, and playing a rig's game in the page, all moved out of WittyMediaServer. Its service answers its own routes at `/api/modules/wittyrigs/...` (`integration.json`), keeps its rigs in a SQLite database of its own (it starts empty: nothing is read from the server's old tables), and makes each rig's library and games through the host API. Needs WittyModule 0.5.4 and a server of module API 5.
- Its screens on every app, TV included: SDUI pages for the Game PCs a person can see, one PC (wake it, end what's running, start a game to resume in Moonlight; for an administrator pairing with its PIN, which apps show, reading its games again, removing it), PCs found on the network to add, and this server's Sunshine. Every row runs one of its own commands.
- Its web part (built by WittyModule's `kit/web` builder): a rig's library, a game's page and the player in the page, and Settings' Game rigs and WittyRigs sections.
- Settings for streams in a page (streams at once, UDP port, public address, STUN) are its own, in every app's Settings, Modules.
- Requires WittyCouch (its protocol code, and WittyCouch Host) and WittyPlay.

- **Module API 4:** carries `sdui.json`, its theme scope (the games accent), and needs WittyMediaServer 0.8.0-alpha or later. It adds no pages of its own: its screens are the server's.
- Finds Sunshine where its installers put it on Windows, Linux (the package and the Flatpak), FreeBSD and macOS (Homebrew too). The server starts this helper with no PATH or ProgramFiles, so before, it only ever found the macOS app.
- Fits servers up to 0.8.x, so WittyMediaServer 0.8.0-alpha accepts it; it was limited to 0.6.x.
- A helper, `wittyrigs-module`, is now how a server reaches WittyRigs, with no copy compiled in.
- WittyRigs is now a project of its own, developed and released from this repository.
