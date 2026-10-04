import { Show } from 'solid-js'
import type { Setter } from 'solid-js'
import type { DownloadJob } from '../types'
import { NavBtn } from '../components/NavBtn'
import { NotificationBell } from '../components/NotificationBell'
import { UserAvatar } from '../components/UserAvatar'
import { UserDropdown } from '../components/UserDropdown'
import { createDownloadQueueStore } from '../hooks/useDownloadQueue'
import { createNotificationsStore } from '../hooks/useNotifications'
import { browserHandlesClick, gridHref } from '../utils/navlink'
import { guardClose as guardCloseGlobal } from '../utils/unsavedChanges'
import { NAV_H, PAGE_X } from '../constants/layout'

export interface NavBarDeps {
  translate: (key: any, params?: any) => string
  user: () => any
  logout: () => void
  search: () => string
  setSearch: (value: string) => void
  hasActive: () => unknown
  viewMode: () => 'grid' | 'list'
  setViewMode: (mode: 'grid' | 'list') => void
  visibleDownloadJobs: () => DownloadJob[]
  hasPlatformAccount: () => boolean
  showUser: () => boolean
  setShowUser: Setter<boolean>
  setShowAdd: Setter<boolean>
  setShowFilters: Setter<boolean>
  setShowSyncAllConfirm: Setter<boolean>
  setShowAccountSettings: Setter<boolean>
  setShowServerSettings: Setter<boolean>
  openCollections: (id?: number | null) => void
  goToGrid: () => void
  downloads: ReturnType<typeof createDownloadQueueStore>
  notifs: ReturnType<typeof createNotificationsStore>
  setUserDropdownRef: (element: HTMLDivElement) => void
}

/**
 * The way home. Shared with the design page, which draws a bar of its own and
 * would otherwise carry a second, slightly different logo.
 *
 * An anchor, not a div: middle click and Ctrl-click then open the grid in a new
 * tab the way any link would. Only the plain left click is taken over, so the
 * in-app navigation stays as it was.
 */
export function navLogo(deps: Pick<NavBarDeps, 'translate' | 'goToGrid'>, fontSize = '23px') {
  return (
    <a href={gridHref()}
      onClick={event => {
        if (browserHandlesClick(event)) return
        event.preventDefault()
        guardCloseGlobal(deps.translate('confirm_discard_changes'), deps.goToGrid)
      }}
      style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':fontSize, color:'var(--text)', 'letter-spacing':'-0.02em', cursor:'pointer', 'user-select':'none', 'text-decoration':'none', 'white-space':'nowrap' }}>
      Mesh<span style={{ color:'var(--accent)' }}>Depot</span>
    </a>
  )
}

/**
 * The two things that are offered wherever one happens to be: notifications and
 * the account. Shared for the same reason as the logo - the design page's bar
 * carries them as well, and a second copy would drift.
 */
export function navGlobalActions(deps: Pick<NavBarDeps, 'translate' | 'user' | 'logout' | 'showUser' | 'setShowUser' | 'setShowAccountSettings' | 'setShowServerSettings' | 'setUserDropdownRef' | 'notifs'>, avatarSize = 44) {
  return (
    <>
      <NotificationBell store={deps.notifs} translate={deps.translate} />
      <div ref={deps.setUserDropdownRef} style={{ position:'relative' }}>
        <button onClick={() => deps.setShowUser(currentValue => !currentValue)}
          style={{ background:'transparent', border:'none', 'border-radius':'50%', width:`${avatarSize}px`, height:`${avatarSize}px`, display:'flex', 'align-items':'center', 'justify-content':'center', cursor:'pointer', padding:'0', overflow:'hidden' }}>
          <UserAvatar name={deps.user()?.name || deps.user()?.email || 'U'} avatarUrl={deps.user()?.avatar_url} size={avatarSize} fontSize={avatarSize > 36 ? 16 : 14} />
        </button>
        <Show when={deps.showUser()}>
          <UserDropdown user={deps.user()!}
            onLogout={() => guardCloseGlobal(deps.translate('confirm_discard_changes'), deps.logout)}
            onAccountSettings={() => { deps.setShowUser(false); deps.setShowAccountSettings(true) }}
            onServerSettings={() => { deps.setShowUser(false); deps.setShowServerSettings(true) }} />
        </Show>
      </div>
    </>
  )
}

/**
 * The sticky navigation bar. A plain function rather than a component, so it
 * keeps running in MainApp's reactive owner exactly as it did inline.
 */
export function navBar(deps: NavBarDeps, hideSearch = false) {
  const { translate, search, setSearch, hasActive, viewMode, setViewMode,
    visibleDownloadJobs, hasPlatformAccount, setShowAdd, setShowFilters,
    setShowSyncAllConfirm, openCollections, downloads } = deps
  return (
    <nav class="stlv-nav" style={{ position:'sticky', top:'0', 'z-index':'100', background:'var(--nav-bg)', 'backdrop-filter':'blur(16px)', 'border-bottom':'1px solid var(--border)', padding:`0 ${PAGE_X}`, height:NAV_H, display:'flex', 'align-items':'center' }}>
      {/* Left - Logo */}
      <div class="stlv-nav-logo" style={{ flex:'1', display:'flex', 'align-items':'center' }}>
        {navLogo(deps)}
      </div>

      {/* Center - Search + Filter (hidden on mobile, drops to second row via flex-wrap) */}
      <Show when={!hideSearch}>
        <div class="stlv-nav-center" style={{ display:'flex', 'align-items':'center', gap:'13px' }}>
          <div style={{ position:'relative', flex:'1' }}>
            <svg style={{ position:'absolute', left:'13px', top:'50%', 'transform':'translateY(-50%)', 'pointer-events':'none' }} width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2.5">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
            </svg>
            <input class="stlv-search" placeholder={translate('nav_search_placeholder')} value={search()} onInput={e => setSearch(e.currentTarget.value)}
              style={{ background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'12px', padding:`11px ${search()?'40px':'19px'} 11px 40px`, color:'var(--text2)', 'font-family':"'DM Sans',sans-serif", 'font-size':'16px', outline:'none', width:'280px' }} />
            <Show when={search()}>
              <button onClick={() => setSearch('')}
                style={{ position:'absolute', right:'11px', top:'50%', 'transform':'translateY(-50%)', background:'none', border:'none', cursor:'pointer', display:'flex', 'align-items':'center', padding:'2px', color:'var(--muted)' }}>
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                </svg>
              </button>
            </Show>
          </div>
          <NavBtn title="Filter" onClick={() => setShowFilters(f => !f)} active={!!hasActive()}>
            <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
              <line x1="4" y1="6" x2="20" y2="6"/><line x1="8" y1="12" x2="16" y2="12"/><line x1="11" y1="18" x2="13" y2="18"/>
            </svg>
          </NavBtn>
        </div>
      </Show>

      {/* Right - desktop buttons + always-visible Add/Bell/Avatar */}
      <div class="stlv-nav-right" style={{ flex:'1', display:'flex', 'align-items':'center', 'justify-content':'flex-end', gap:'13px' }}>
        <Show when={!hideSearch}>
          {/* Secondary actions hidden on mobile */}
          <div class="stlv-nav-extra" style={{ display:'contents' }}>
            <Show when={visibleDownloadJobs().some(j => j.status === 'failed')}>
              <NavBtn title={translate('download_retry_all_failed')} onClick={() => downloads.retryAll(visibleDownloadJobs().filter(job => job.status === 'failed'))} active>
                <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="var(--danger)" stroke-width="2.2">
                  <path d="M1 4v6h6"/><path d="M23 20v-6h-6"/>
                  <path d="M20.49 9A9 9 0 0 0 5.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 0 1 3.51 15"/>
                </svg>
              </NavBtn>
            </Show>
            <NavBtn title={translate('collection_title')} onClick={() => openCollections()}>
              <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
              </svg>
            </NavBtn>
            <Show when={hasPlatformAccount()}>
              <NavBtn title={translate('nav_sync_all')} onClick={() => setShowSyncAllConfirm(true)}>
                <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                  <path d="M1 4v6h6"/><path d="M23 20v-6h-6"/>
                  <path d="M20.49 9A9 9 0 0 0 5.64 5.64L1 10M23 14l-4.64 4.36A9 9 0 0 1 3.51 15"/>
                </svg>
              </NavBtn>
            </Show>
            <NavBtn
              title={viewMode() === 'grid' ? translate('nav_view_list') : translate('nav_view_grid')}
              onClick={() => { const m = viewMode() === 'grid' ? 'list' : 'grid'; setViewMode(m); localStorage.setItem('stlv_view_mode', m) }}>
              <Show when={viewMode() === 'grid'} fallback={
                <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                  <rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/>
                  <rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/>
                </svg>
              }>
                <svg width="19" height="19" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                  <line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/>
                </svg>
              </Show>
            </NavBtn>
          </div>
          <NavBtn title={translate('nav_add_design')} onClick={() => setShowAdd(true)}>
            <svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
            </svg>
          </NavBtn>
        </Show>

        {navGlobalActions(deps)}
      </div>
    </nav>
  )
}
