// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
//
// WittyRigs' screens in WittyMediaServer's web app (module API 5; integration.json says what it
// claims): a gaming PC's library (the app sends a rig's library here), a game's page, playing it in
// the page, and its two sections of Settings. The TV apps draw its SDUI pages instead (sdui.json).
import { Suspense, type ReactNode } from 'react'
import { Spinner } from '@wms/app/components/common'
import { RigGamePage, RigLibraryPage, RigPlayPage } from './pages'
import { GameRigsSettings, WittyRigsSettings } from './settings'

const wait = (children: ReactNode) => <Suspense fallback={<Spinner />}>{children}</Suspense>

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function register(_host: unknown): any {
  return {
    routes: {
      '/rigs/library/:id': () => wait(<RigLibraryPage />),
      '/rigs/game/:id': () => wait(<RigGamePage />),
      '/rigs/play/:id': () => wait(<RigPlayPage />),
    },
    settings: {
      gamerigs: () => wait(<GameRigsSettings />),
      wittyrigs: () => wait(<WittyRigsSettings />),
    },
  }
}
