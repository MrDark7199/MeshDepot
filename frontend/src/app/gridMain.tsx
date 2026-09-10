import { For, Show } from 'solid-js'
import type { Setter } from 'solid-js'
import type { Design, DownloadJob, Filters } from '../types'
import { DesignCard } from '../components/DesignCard'
import { DesignRow } from '../components/DesignRow'
import { DownloadPlaceholderCard, DownloadPlaceholderRow } from '../components/DownloadPlaceholder'
import { createDownloadQueueStore } from '../hooks/useDownloadQueue'
import { createSyncProgressStore } from '../hooks/useSyncProgress'
import { NAV_H } from '../constants/layout'

export type SortField = 'name' | 'platform' | 'updated_at'

export interface GridMainDeps {
  translate: (key: any, params?: any) => string
  loading: () => boolean
  designs: () => Design[]
  sortedDesigns: () => Design[]
  visibleDownloadJobs: () => DownloadJob[]
  viewMode: () => 'grid' | 'list'
  sortField: () => SortField
  sortDir: () => 'asc' | 'desc'
  toggleSort: (field: SortField) => void
  page: () => number
  perPage: () => number
  totalDesigns: () => number
  setPage: (page: number) => void
  loadDesigns: (query?: string, filters?: Filters, page?: number, perPage?: number) => void
  search: () => string
  filters: () => Filters
  openDesign: (design: Design) => void
  syncOne: (design: Design) => void
  setAccountSettingsTab: (tab: string) => void
  setShowAccountSettings: Setter<boolean>
  downloads: ReturnType<typeof createDownloadQueueStore>
  sync: ReturnType<typeof createSyncProgressStore>
}

/**
 * The grid page itself: sort bar, cards or rows, and the pagination below them.
 * A plain function like navBar, for the same reason.
 */
export function gridMain(deps: GridMainDeps) {
  const { translate, loading, designs, sortedDesigns, visibleDownloadJobs, viewMode, sortField,
    sortDir, toggleSort, page, perPage, totalDesigns, setPage, loadDesigns, search, filters,
    openDesign, syncOne, setAccountSettingsTab, setShowAccountSettings, downloads, sync } = deps
  return (
        <main class="stlv-main" style={{ padding: '54px 40px' }}>
          <Show when={loading()} fallback={<>
            {/* Sort bar */}
            <Show when={designs().length > 0}>
              <div style={{ display:'flex', 'align-items':'center', 'justify-content': viewMode()==='grid' ? 'center' : 'flex-start', gap:'6px', 'max-width': viewMode()==='list' ? '1200px' : '1600px', margin:'0 auto 18px', 'flex-wrap':'wrap' }}>
                <span style={{ 'font-family':"'DM Mono',monospace", 'font-size':'11px', color:'var(--muted)', 'white-space':'nowrap', 'padding-right':'4px' }}>{translate('sort_by')}</span>
                {(['name','platform','updated_at'] as const).map(field => (
                  <button onClick={() => toggleSort(field)}
                    style={{ padding:'4px 11px', 'border-radius':'7px', 'font-family':"'DM Mono',monospace", 'font-size':'11px', cursor:'pointer',
                      border: sortField()===field ? '1px solid var(--accent)' : '1px solid var(--border)',
                      background: sortField()===field ? 'rgba(69,123,157,0.15)' : 'var(--surface)',
                      color: sortField()===field ? 'var(--accent-light)' : 'var(--text2)',
                      'font-weight': sortField()===field ? '700' : '400' }}>
                    {translate(`sort_${field}` as any)}{sortField()===field ? (sortDir()==='asc' ? ' ↑' : ' ↓') : ''}
                  </button>
                ))}
              </div>
            </Show>
            <Show when={viewMode() === 'grid'} fallback={
              <div style={{ display:'flex', 'flex-direction':'column', gap:'6px', 'max-width':'1200px', margin:'0 auto' }}>
                <For each={page() === 1 ? visibleDownloadJobs() : []}>{(job) => <DownloadPlaceholderRow job={job} onCancel={() => downloads.cancel(job.id)} onRetry={() => downloads.retry(job.id)} onDismiss={() => downloads.dismiss(job.id)} onOpenSettings={() => { setAccountSettingsTab('platforms'); setShowAccountSettings(true) }} />}</For>
                <For each={sortedDesigns()}>{(design, designIndex) =>
                  <DesignRow design={design} index={designIndex()} onOpen={openDesign} onSync={syncOne} syncStatus={sync.statusMap()[design.id]} syncProgress={sync.progressMap()[design.id]} syncStep={sync.stepMap()[design.id]} />
                }</For>
                <Show when={designs().length === 0 && visibleDownloadJobs().length === 0}>
                  <div style={{ color: 'var(--muted3)', 'font-family': "'DM Mono',monospace", 'font-size': '18px', 'margin-top': '110px', 'text-align': 'center', width: '100%' }}>
                    <div style={{ 'font-size': '64px', 'margin-bottom': '22px', opacity: '0.4' }}>🖨️</div>
                    <div>{search() ? translate('nav_no_results', {query: search()}) : translate('nav_no_designs')}</div>
                  </div>
                </Show>
              </div>
            }>
            <div class="stlv-grid" style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '32px', 'justify-content': 'center', 'max-width': '1600px', margin: '0 auto' }}>
              <For each={page() === 1 ? visibleDownloadJobs() : []}>{(job) => <DownloadPlaceholderCard job={job} onCancel={() => downloads.cancel(job.id)} onRetry={() => downloads.retry(job.id)} onDismiss={() => downloads.dismiss(job.id)} onOpenSettings={() => { setAccountSettingsTab('platforms'); setShowAccountSettings(true) }} />}</For>
              <For each={sortedDesigns()}>{(design, designIndex) =>
                <DesignCard design={design} index={designIndex()} onOpen={openDesign} onSync={syncOne} syncStatus={sync.statusMap()[design.id]} syncProgress={sync.progressMap()[design.id]} syncStep={sync.stepMap()[design.id]} />
              }</For>
              <Show when={designs().length === 0 && visibleDownloadJobs().length === 0}>
                <div style={{ color: 'var(--muted3)', 'font-family': "'DM Mono',monospace", 'font-size': '18px', 'margin-top': '110px', 'text-align': 'center', width: '100%' }}>
                  <div style={{ 'font-size': '64px', 'margin-bottom': '22px', opacity: '0.4' }}>🖨️</div>
                  <div>{search() ? translate('nav_no_results', {query: search()}) : translate('nav_no_designs')}</div>
                </div>
              </Show>
            </div>
            </Show>
            <Show when={totalDesigns() > perPage()}>
              {(() => {
                const totalPages = () => Math.ceil(totalDesigns() / perPage())
                const goTo = (p: number) => { setPage(p); loadDesigns(search(), filters(), p) }
                const pageButton = (p: number) => (
                  <button onClick={() => goTo(p)}
                    style={{ 'min-width':'36px', height:'36px', padding:'0 10px', 'border-radius':'9px', border:`1px solid ${page()===p?'var(--accent)':'var(--border)'}`, background:page()===p?'rgba(69,123,157,0.2)':'var(--surface)', color:page()===p?'var(--accent-light)':'var(--text2)', cursor:'pointer', 'font-family':"'DM Mono',monospace", 'font-size':'13px', 'font-weight':page()===p?'700':'400' }}>
                    {p}
                  </button>
                )
                return (
                  <div style={{ display:'flex', 'align-items':'center', 'justify-content':'center', gap:'8px', 'margin-top':'48px', 'flex-wrap':'wrap' }}>
                    <button onClick={() => goTo(Math.max(1, page()-1))} disabled={page()===1}
                      style={{ height:'36px', padding:'0 14px', 'border-radius':'9px', border:'1px solid var(--border)', background:'var(--surface)', color:page()===1?'var(--muted)':'var(--text2)', cursor:page()===1?'default':'pointer', 'font-family':"'DM Mono',monospace", 'font-size':'13px' }}>
                      ←
                    </button>
                    {(() => {
                      const total = totalPages()
                      const cur = page()
                      const pages: (number|'…')[] = []
                      const add = (p: number) => { if (!pages.includes(p)) pages.push(p) }
                      add(1)
                      for (let i = Math.max(2, cur-1); i <= Math.min(total-1, cur+1); i++) add(i)
                      add(total)
                      const withDots: (number|'…')[] = []
                      let prev = 0
                      for (const pageNumber of pages) {
                        if (typeof pageNumber === 'number') {
                          if (prev && pageNumber - prev > 1) withDots.push('…')
                          withDots.push(pageNumber)
                          prev = pageNumber
                        }
                      }
                      return <For each={withDots}>{item => item === '…'
                        ? <span style={{ color:'var(--muted)', 'font-family':"'DM Mono',monospace", 'font-size':'13px', padding:'0 4px' }}>…</span>
                        : pageButton(item as number)
                      }</For>
                    })()}
                    <button onClick={() => goTo(Math.min(totalPages(), page()+1))} disabled={page()===totalPages()}
                      style={{ height:'36px', padding:'0 14px', 'border-radius':'9px', border:'1px solid var(--border)', background:'var(--surface)', color:page()===totalPages()?'var(--muted)':'var(--text2)', cursor:page()===totalPages()?'default':'pointer', 'font-family':"'DM Mono',monospace", 'font-size':'13px' }}>
                      →
                    </button>
                    <span style={{ 'font-family':"'DM Mono',monospace", 'font-size':'12px', color:'var(--muted)', 'margin-left':'8px' }}>
                      {(page()-1)*perPage()+1}–{Math.min(page()*perPage(), totalDesigns())} / {totalDesigns()}
                    </span>
                  </div>
                )
              })()}
            </Show>
          </>}>
            {/* Centred in the space below the nav rather than pushed down by a
                fixed margin. The old 110px was added on top of the nav and the
                main padding without regard to the window, so on a short enough
                one the spinner alone made the page scrollable. */}
            <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'center', height: `calc(100vh - ${NAV_H} - 108px)`, 'min-height': '160px' }}>
              <div style={{ width: '40px', height: '40px', border: '3px solid var(--border)', 'border-top': '3px solid var(--accent)', 'border-radius': '50%', animation: 'spin 0.8s linear infinite' }} />
            </div>
          </Show>
        </main>
  )
}
