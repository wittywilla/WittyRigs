// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors
import { useEffect, useRef, useState } from 'react'
import { api, ApiError } from '@wms/app/api'
import { LoadError, Spinner } from '@wms/app/components/common'
import LegalNote from '@wms/app/components/LegalNote'
import { toast, useApp } from '@wms/app/state'
import { PageHead, Section } from '@wms/app/pages/settings/ui'
import { WebLink } from '@wms/app/components/WebLink'
import { RIGS, type RigView } from './pages'
import './rigs.css'

// WittyRigs' sections of Settings: Game rigs (adding a gaming PC, pairing it, and choosing which of
// its apps show in its library) and WittyRigs (this server's own Sunshine). Sunshine and Moonlight
// are separate projects, and these pages only ever talk to them.

type Found = { name: string; address: string; port: number; version: string; added: boolean }
type Pairing = { state: string; pin?: string; message?: string; kind?: string }
type App = { id: number; title: string; shown: boolean; itemId: number | null }

export function SetupHelp() {
  return (
    <details>
      <summary>How to install Sunshine on that PC</summary>
      <ol className="rig-steps">
        <li>On the gaming PC, download Sunshine from <a href="https://github.com/LizardByte/Sunshine/releases/latest" target="_blank" rel="noreferrer noopener">its own releases page</a>. On Windows, choose <code>Sunshine-Windows-AMD64-installer.msi</code>. Linux and Mac versions are there too, and <a href="https://docs.lizardbyte.dev/projects/sunshine/latest/md_docs_2getting__started.html" target="_blank" rel="noreferrer noopener">its guide</a> covers them.</li>
        <li>Install it. Windows will ask for permission, as it sets itself up as a service and adds a virtual controller driver.</li>
        <li>On that PC, open <code>https://localhost:47990</code>. Your browser warns about the certificate, which is normal here. Choose a user name and password for Sunshine, and keep them somewhere safe.</li>
        <li>Come back here and press Find, or type the PC’s address. Then you’ll be given a PIN to type into Sunshine’s PIN page on that PC.</li>
        <li>To wake the PC from here later, turn on Wake-on-LAN in the PC’s BIOS and in its network adapter’s settings. It usually needs a wired connection.</li>
      </ol>
      <p className="muted">Sunshine is made by LizardByte and licensed under the GPL-3.0. It isn’t part of this server, and this server contains none of its code.</p>
    </details>
  )
}

export function AddRig({ onAdded, title, desc }: { onAdded: () => void; title: string; desc: string }) {
  const [address, setAddress] = useState('')
  const [finding, setFinding] = useState(false)
  const [adding, setAdding] = useState(false)
  const [found, setFound] = useState<Found[] | null>(null)
  const [publicWarning, setPublicWarning] = useState('')
  const asked = useRef('') // the address the warning is about, which may have come from the found list
  const find = async () => {
    setFinding(true)
    try { setFound(await api.get<Found[]>(`${RIGS}/rigs/find`)) } catch (e: any) { toast(e.message) } finally { setFinding(false) }
  }
  const add = async (addr: string, confirmPublic = false) => {
    asked.current = addr
    setAdding(true)
    try {
      await api.post(`${RIGS}/rigs`, { address: addr, confirmPublic })
      setAddress(''); setPublicWarning(''); setFound(null)
      toast('Added. Now pair it.')
      onAdded()
    } catch (e: any) {
      if (e.data?.public) setPublicWarning(e.message)
      else toast(e.message)
    } finally { setAdding(false) }
  }
  return (
    <Section title={title} desc={desc}>
      <SetupHelp />
      <div className="rig-row-actions">
        <button className="btn" disabled={finding} onClick={find}>{finding ? 'Looking on your network…' : 'Find PCs on my network'}</button>
      </div>
      {found && (found.length === 0
        ? <p className="muted">None answered. Sunshine may not be running, or your network may not pass this kind of search: type its address instead.</p>
        : <ul className="rig-found">{found.map((f) => (
            <li key={f.address}><span><b>{f.name}</b> <span className="muted">{f.address}</span></span>
              {f.added ? <span className="muted">Already added</span> : <button className="btn sm" disabled={adding} onClick={() => add(`${f.address}:${f.port}`)}>Add</button>}
            </li>))}
          </ul>)}
      <form className="rig-form" onSubmit={(e) => { e.preventDefault(); if (address.trim()) add(address.trim()) }}>
        <label>Or type its address
          <input className="input" value={address} onChange={(e) => setAddress(e.target.value)} placeholder="192.168.1.20 or gaming-pc" autoCapitalize="off" autoCorrect="off" spellCheck={false} />
        </label>
        <div><button className="btn" disabled={adding || !address.trim()}>{adding ? 'Checking…' : 'Add this PC'}</button></div>
      </form>
      {publicWarning && (
        <div className="rig-warn" role="alert">
          <p>{publicWarning}</p>
          <button className="btn sm" onClick={() => add(asked.current, true)}>Add it anyway</button>
        </div>
      )}
    </Section>
  )
}

export function RigCard({ rig, onChange }: { rig: RigView; onChange: () => void }) {
  const [pairing, setPairing] = useState<Pairing | null>(null)
  const [apps, setApps] = useState<App[] | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [remote, setRemote] = useState(rig.remoteAddress || '')
  const url = `${RIGS}/rigs/${rig.id}`
  const loadApps = () => api.get<App[]>(`${url}/apps`).then(setApps).catch(() => {})
  useEffect(() => { loadApps() }, [rig.id, rig.paired]) // eslint-disable-line react-hooks/exhaustive-deps
  // While a PIN is waiting, watch for it being typed.
  useEffect(() => {
    if (pairing?.state !== 'waiting') return
    const t = window.setInterval(async () => {
      const p = await api.get<Pairing>(`${url}/pair`).catch(() => null)
      if (!p) return
      setPairing(p)
      if (p.state !== 'waiting') { onChange(); loadApps() }
    }, 1500)
    return () => clearInterval(t)
  }, [pairing?.state]) // eslint-disable-line react-hooks/exhaustive-deps
  const startPair = async () => {
    try { setPairing(await api.post<Pairing>(`${url}/pair`)) } catch (e: any) { toast(e.message) }
  }
  const cancel = async () => { await api.del(`${url}/pair`).catch(() => {}); setPairing(null) }
  const sync = async () => {
    try { setApps(await api.post<App[]>(`${url}/sync`)); toast('Games updated') } catch (e: any) { toast(e.message) }
  }
  const toggle = async (a: App) => {
    try { setApps(await api.put<App[]>(`${url}/apps/${a.id}`, { shown: !a.shown })) } catch (e: any) { toast(e.message) }
  }
  const saveRemote = async () => {
    try { await api.patch(url, { remoteAddress: remote }); toast('Saved'); onChange() } catch (e: any) { toast(e.message) }
  }
  const remove = async () => {
    try {
      const r = await api.del<{ note?: string }>(url)
      toast(r?.note || 'Removed')
      onChange()
    } catch (e: any) { toast(e.message) }
  }
  return (
    <Section title={rig.name} desc={[rig.address && (rig.port ? `${rig.address}:${rig.port}` : rig.address), rig.version && `Sunshine host ${rig.version}`].filter(Boolean).join(' · ')}>
      <div className={'rig-status' + (rig.state === 'online' ? ' ok' : '')} role="status"><span className="rig-dot" aria-hidden="true" /><div className="grow">{rig.message}</div></div>
      {rig.public && <p className="rig-warn">This PC’s address is on the internet, so its streaming ports are open to anyone who finds them. A private network is much safer.</p>}

      {pairing?.state === 'waiting' && (
        <div className="rig-warn" role="status">
          <p>On that PC, open Sunshine{rig.webUrl ? <> at <code>{rig.webUrl}</code></> : ''}, go to <b>PIN</b>, and type:</p>
          <div className="rig-pin" aria-label={`PIN ${pairing.pin?.split('').join(' ')}`}>{pairing.pin}</div>
          <p className="muted">Waiting for it to be typed. Do this on your home network.</p>
          <button className="btn sm ghost" onClick={cancel}>Cancel</button>
        </div>
      )}
      {pairing && pairing.state !== 'waiting' && pairing.message && <p className={pairing.state === 'failed' ? 'rig-warn' : ''} role="status">{pairing.message}</p>}

      <div className="rig-row-actions">
        {(!rig.paired || rig.state === 'not-paired' || rig.state === 'cert-changed') && pairing?.state !== 'waiting' && <button className="btn primary" onClick={startPair}>{rig.paired ? 'Pair again' : 'Pair this PC'}</button>}
        {rig.paired && <button className="btn sm" onClick={sync}>Read its games again</button>}
        {!confirming && <button className="btn sm ghost" onClick={() => setConfirming(true)}>Remove</button>}
      </div>
      {confirming && (
        <div className="rig-warn" role="alert">
          <p>Remove {rig.name} and its library from this server? Nothing changes on the PC itself: to finish, remove this server from the paired devices in Sunshine there.</p>
          <div className="rig-row-actions">
            <button className="btn sm" onClick={remove}>Remove it</button>
            <button className="btn sm ghost" onClick={() => setConfirming(false)}>Keep it</button>
          </div>
        </div>
      )}

      {apps && apps.length > 0 && (
        <details open={apps.length < 12}>
          <summary>What shows in your library ({apps.filter((a) => a.shown).length} of {apps.length})</summary>
          <p className="muted">Anyone who can see this library can play what’s shown here. Kids and Guest profiles see nothing until you give them a game in Profiles. Remember that keyboard and mouse can often get out of a game to the PC’s desktop, which holds whatever that PC does: allow people accordingly. Desktop apps start hidden for this reason.</p>
          <ul className="rig-apps">
            {apps.map((a) => (
              <li key={a.id} className="rig-app"><span>{a.title}</span>
                <label><input type="checkbox" checked={a.shown} onChange={() => toggle(a)} /> Show</label>
              </li>
            ))}
          </ul>
        </details>
      )}

      {rig.paired && <DevicePIN url={url} />}

      <details>
        <summary>Address away from home</summary>
        <form className="rig-form" onSubmit={(e) => { e.preventDefault(); saveRemote() }}>
          <label>This PC’s address on a private network such as Tailscale or ZeroTier. Moonlight apps use it when they’re away from home.
            <input className="input" value={remote} onChange={(e) => setRemote(e.target.value)} placeholder="100.64.0.5" autoCapitalize="off" autoCorrect="off" spellCheck={false} />
          </label>
          <div><button className="btn sm">Save</button></div>
        </form>
      </details>
    </Section>
  )
}

// Typing a PIN for someone's Moonlight app, without walking to the PC.
function DevicePIN({ url }: { url: string }) {
  const [f, setF] = useState({ pin: '', device: '', username: '', password: '' })
  const [busy, setBusy] = useState(false)
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value })
  const send = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      await api.post(`${url}/devices`, f)
      setF({ pin: '', device: '', username: f.username, password: '' })
      toast('Paired')
    } catch (err: any) { toast(err.message) } finally { setBusy(false) }
  }
  return (
    <details>
      <summary>Pair a Moonlight app from here</summary>
      <p className="muted">A device pairs once: Moonlight shows a PIN, and it goes into Sunshine. You can type it there, or here. <b>A paired Moonlight app can reach everything on that PC, including things hidden from your library,</b> and this server can’t limit it, so only pair devices you’d hand the PC’s keyboard to.</p>
      <p className="muted">Your Sunshine password goes straight to that PC for this one request. It isn’t saved.</p>
      <form className="rig-form" onSubmit={send}>
        <label>PIN shown by Moonlight<input className="input" inputMode="numeric" maxLength={4} value={f.pin} onChange={set('pin')} /></label>
        <label>Device name, if several are waiting<input className="input" value={f.device} onChange={set('device')} /></label>
        <label>Sunshine user name<input className="input" autoComplete="off" value={f.username} onChange={set('username')} /></label>
        <label>Sunshine password<input className="input" type="password" autoComplete="off" value={f.password} onChange={set('password')} /></label>
        <div><button className="btn sm" disabled={busy || f.pin.length !== 4 || !f.password}>{busy ? 'Sending…' : 'Pair the device'}</button></div>
      </form>
    </details>
  )
}


const isLocal = (r: RigView) => r.address === '127.0.0.1' || r.address === 'localhost' || r.address === '::1'

// Settings, Game rigs: gaming PCs shared with this server's people.
export function GameRigsSettings() {
  const [rigs, setRigs] = useState<RigView[] | null>(null)
  const [failed, setFailed] = useState(false)
  const { refreshLibraries } = useApp()
  const load = () => { setFailed(false); api.get<RigView[]>(`${RIGS}/rigs`).then(setRigs).catch(() => setFailed(true)) }
  useEffect(load, [])
  const changed = () => { load(); void refreshLibraries() }
  if (failed) return <LoadError what="Game rigs" onRetry={load} />
  if (!rigs) return <Spinner />
  return (
    <>
      <PageHead title="Game rigs" desc="A gaming PC running Sunshine, paired once by you, whose games become a library. Everyone’s limits apply, so a Kids profile can be given single games from it. Streams in the page, and the ports they use, are set in Settings, Modules, Game rigs." />
      <AddRig onAdded={changed} title="Share a PC with this server’s people" desc="Its games become a library, played in the page or in Moonlight." />
      {rigs.filter((r) => !isLocal(r)).map((r) => <RigCard key={r.id} rig={r} onChange={changed} />)}
      <LegalNote kind="thirdPartyServer" className="set-legal" />
    </>
  )
}

type Status = { enabled: boolean; installed: string; running: boolean; rig?: RigView; license: string; source: string }

export function WittyRigsSettings() {
  const [st, setSt] = useState<Status | null>(null)
  const [busy, setBusy] = useState(false)
  const [sure, setSure] = useState(false)
  const { refreshLibraries } = useApp()
  const [failed, setFailed] = useState(false)
  // With the module missing this says so and offers it; LoadError knows which module it was.
  const load = () => { setFailed(false); return api.get<Status>(`${RIGS}/local`).then(setSt).catch((e) => { if (e instanceof ApiError) setFailed(true); else toast(e.message) }) }
  useEffect(() => { load() }, [])
  const set = async (enabled: boolean) => {
    setBusy(true); setSure(false)
    try { setSt(await api.put<Status>(`${RIGS}/local`, { enabled })); void refreshLibraries() } catch (e: any) { toast(e.message) } finally { setBusy(false) }
  }
  if (failed) return <LoadError what="WittyRigs" onRetry={load} />
  if (!st) return <Spinner />
  return (
    <>
      <PageHead title="WittyRigs" desc="Advanced, and most servers don’t need it. It’s for playing demanding PC games installed on this server itself, using this machine’s graphics card. To share a gaming PC of yours, use Game rigs." />
      <Section title="Hosting games on this server">
        {!st.enabled && !sure && (
          <>
            <p>This uses <b>Sunshine</b>, a separate program you install on this machine. It streams this machine’s screen, and everything on it, to whoever plays, and adds a service and virtual controller drivers. It needs a good graphics card, and a screen or a virtual one. Sunshine is GPL-3.0 software, with its own license and source.</p>
            <button className="btn" disabled={busy} onClick={() => setSure(true)}>Turn on</button>
          </>
        )}
        {sure && (
          <div className="rig-warn" role="alert">
            <p>Sunshine is GPL-3.0 software by LizardByte, installed separately from <WebLink href={st.source}>its own project</WebLink>. This server only talks to it over the network. Anyone you give a game from it can often get from that game to this machine’s desktop, so give them out with care.</p>
            <div className="rig-row-actions">
              <button className="btn sm" onClick={() => set(true)}>Turn it on</button>
              <button className="btn sm ghost" onClick={() => setSure(false)}>Not now</button>
            </div>
          </div>
        )}
        {st.enabled && (
          <>
            <p>{st.running ? 'Sunshine is running on this machine.' : st.installed ? <>Sunshine is installed at <code>{st.installed}</code>, but isn’t running. Start it, then come back.</> : 'Sunshine isn’t installed on this machine, or isn’t running.'}</p>
            {!st.running && <SetupHelp />}
            <div className="rig-row-actions"><button className="btn sm ghost" disabled={busy} onClick={() => set(false)}>Turn off and remove its library</button></div>
          </>
        )}
      </Section>
      {st.enabled && st.rig && <RigCard rig={st.rig} onChange={load} />}
      <Section title="License"><p>WittyRigs is AGPL-3.0-or-later. Sunshine itself is {st.license}: <WebLink href={st.source}>source</WebLink>. Neither contains the other.</p></Section>
      <LegalNote kind="games" className="set-legal" />
    </>
  )
}
