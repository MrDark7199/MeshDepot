import { AddDesignsToCollectionModal } from './AddDesignsToCollectionModal'
import { DesignCard } from './DesignCard'
import { DesignPage } from './DesignPage'
import { FilterModal } from './FilterModal'
import { PLATFORM_COLORS, platformLabel } from '../constants/platforms'
import { useI18n } from '../i18n/index'
import { api } from '../services/api'
import { For, Show, createEffect, createSignal, on } from 'solid-js'
import type { Collection, Design, DesignID, Filters, Tag } from '../types'
import { errorKey } from '../utils/errorMessage'

/**
 * Collections management page - sidebar with collection list + create form,
 * and a content area showing the selected collection's designs with search
 * and filter controls.
 */
export function CollectionsPage(props: {
  collections: Collection[]
  onBack: () => void
  showToast: (m: string, v?: string) => void
  onChanged: () => void
  onOpenDesign: (id: DesignID) => void
  initialCollectionId?: number | null
  onInitialCollectionHandled?: () => void
}) {
  const {translate} = useI18n()
  const [activeCol, setActiveCol] = createSignal<Collection | null>(null)
  const [colDesigns, setColDesigns] = createSignal<Design[]>([])
  const [showAddDesigns, setShowAddDesigns] = createSignal(false)
  const [loadingDesigns, setLoadingDesigns] = createSignal(false)
  const [newColName, setNewColName] = createSignal('')
  const [isCreating, setIsCreating] = createSignal(false)
  const [pendingDeleteCol, setPendingDeleteCol] = createSignal<Collection | null>(null)
  // Renaming the open collection, in place in the heading. Synced collections
  // are renamed the same way as own ones: the sync keeps the local name.
  const [renamingCol, setRenamingCol] = createSignal(false)
  const [renameDraft, setRenameDraft] = createSignal('')
  // Per-collection filters
  const [colSearch, setColSearch] = createSignal('')
  const [colFilters, setColFilters] = createSignal<Filters>({source_platform:'', tag_ids:[], shared_only:false, show_hidden:false})
  const [showColFilter, setShowColFilter] = createSignal(false)
  const [allTags, setAllTags] = createSignal<Tag[]>([])

  const sans = "'DM Sans',sans-serif"
  const mono = "'DM Mono',monospace"
  const inp: any = { width:'100%', background:'var(--input-bg)', border:'1px solid var(--border2)', 'border-radius':'10px', padding:'10px 14px', color:'var(--text)', 'font-family':sans, 'font-size':'14px', outline:'none', 'box-sizing':'border-box', 'margin-bottom':'11px' }

  // Load tags for filter
  createEffect(() => {
    api.getTags().then((r: any) => setAllTags(r.data || [])).catch(() => {})
  })

  // The collection tab lists hidden collections too - unlike the filter and the
  // rest of the app - so they can be unhidden again. It therefore loads its own
  // list with include_hidden, separate from the parent's hidden-excluded one.
  const [cols, setCols] = createSignal<Collection[]>(props.collections)
  const loadCols = () => api.getCollections(true).then((r: any) => setCols(r.data || [])).catch(() => {})
  createEffect(() => { loadCols() })
  // Refresh both this tab's hidden-inclusive list and the parent's (which feeds
  // the filter and grid), so a hide/unhide/rename shows everywhere at once.
  const refreshCols = () => { loadCols(); props.onChanged() }

  const openCollection = async (col: Collection) => {
    setActiveCol(col); setLoadingDesigns(true); setColSearch(''); setColFilters({source_platform:'', tag_ids:[], shared_only:false, show_hidden:false})
    try { const response = await api.getCollectionDesigns(col.id) as any; setColDesigns(response.data || []) }
    catch {} finally { setLoadingDesigns(false) }
  }

  // Auto-open a specific collection when navigated from DesignPage
  createEffect(() => {
    const id = props.initialCollectionId
    if (!id) return
    const col = cols().find(c => c.id === id)
    if (col) { openCollection(col); props.onInitialCollectionHandled?.() }
  })

  /**
   * Returns the active collection's designs filtered by search query,
   * platform, tags, and shared-only flag.
   */
  const filteredColDesigns = () => {
    let designs = colDesigns()
    const query = colSearch().toLowerCase().trim()
    const filters = colFilters()
    if (query) designs = designs.filter(design => design.name.toLowerCase().includes(query) || (design.name_de || '').toLowerCase().includes(query) || (design.author || '').toLowerCase().includes(query))
    if (filters.source_platform) designs = designs.filter(design => design.source_platform === filters.source_platform)
    if (filters.tag_ids.length > 0) designs = designs.filter(design => filters.tag_ids.every(tid => design.tags?.some(tag => tag.id === tid)))
    if (filters.shared_only) designs = designs.filter(design => design.is_shared)
    if (!filters.show_hidden) designs = designs.filter(design => !design.is_hidden)
    return designs
  }

  const colHasActiveFilters = () => { const filters = colFilters(); return !!(filters.source_platform || filters.tag_ids.length || filters.shared_only || filters.show_hidden) }

  const createCollection = async () => {
    const name = newColName().trim()
    if (!name) return; setIsCreating(true)
    try {
      const created: any = await api.createCollection({ name })
      setNewColName('')
      // Show the new collection in the sidebar right away, in its sorted spot,
      // instead of waiting for the reload round-trip.
      if (created?.data?.id) {
        setCols(previous => [...previous, created.data as Collection].sort((a, b) => a.name.localeCompare(b.name)))
      }
      props.showToast(translate('toast_collection_created'))
      refreshCols()
    }
    catch (failure: unknown) { props.showToast(translate(errorKey(failure)), 'error') }
    finally { setIsCreating(false) }
  }

  const deleteCollection = async (id: number) => {
    try { await api.deleteCollection(id); props.showToast(translate('toast_collection_deleted')); refreshCols(); if (activeCol()?.id === id) setActiveCol(null) } catch {}
  }

  const startRename = () => { const col = activeCol(); if (!col) return; setRenameDraft(col.name); setRenamingCol(true) }

  /**
   * Writes the edited name. Closing the editor first makes this safe to call
   * twice: pressing Enter saves and unmounts the input, whose blur would
   * otherwise arrive right behind it and save a second time.
   */
  const commitRename = async () => {
    if (!renamingCol()) return
    setRenamingCol(false)
    const col = activeCol()
    const name = renameDraft().trim()
    if (!col || !name || name === col.name) return
    try {
      await api.updateCollection(col.id, { name })
      // The local copy drives the heading, so it is updated alongside the
      // reload: without it the heading falls back to the old name until the
      // collection list has come back.
      setActiveCol({ ...col, name })
      props.showToast(translate('toast_collection_renamed'))
      refreshCols()
    } catch {}
  }

  // Hiding keeps the collection and its designs; it only takes it out of the
  // filter, a design's details and the normal lists. This tab still shows hidden
  // collections (marked), which is the way back to unhiding one.
  const toggleHidden = async (col: Collection) => {
    const nowHidden = !col.is_hidden
    try {
      await api.updateCollection(col.id, { is_hidden: nowHidden })
      if (activeCol()?.id === col.id) setActiveCol({ ...col, is_hidden: nowHidden })
      props.showToast(translate(nowHidden ? 'toast_collection_hidden' : 'toast_collection_unhidden'))
      refreshCols()
    } catch {}
  }

  return (
    <div style={{ 'min-height':'100vh', background:'var(--bg)', padding:'36px' }}>
      <div style={{ 'max-width':'1400px', margin:'0 auto' }}>
        <div style={{ display:'flex', 'align-items':'center', gap:'18px', 'margin-bottom':'34px' }}>
          <button onClick={props.onBack}
            style={{ background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'11px', padding:'9px 18px', color:'var(--text2)', cursor:'pointer', 'font-family':sans, 'font-size':'15px', 'font-weight':'600', display:'flex', 'align-items':'center', gap:'7px' }}>
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 18 9 12 15 6"/></svg>
            {translate('btn_back')}
          </button>
          <h1 style={{ 'font-family':sans, 'font-size':'26px', 'font-weight':'700', color:'var(--text)' }}>{translate('collection_title')}</h1>
        </div>

        <div style={{ display:'grid', 'grid-template-columns':'300px 1fr', gap:'26px' }}>
          {/* Sidebar */}
          <div>
            <div style={{ background:'var(--bg2)', border:'1px solid var(--border)', 'border-radius':'15px', padding:'18px', 'margin-bottom':'18px' }}>
              <input value={newColName()} onInput={e => setNewColName(e.currentTarget.value)}
                placeholder={translate('collection_name')}
                onKeyDown={(e: KeyboardEvent) => e.key === 'Enter' && createCollection()}
                style={inp} />
              <button onClick={createCollection} disabled={!newColName().trim() || isCreating()}
                style={{ width:'100%', padding:'10px', background:'var(--accent)', border:'none', 'border-radius':'10px', color:'#fff', 'font-family':sans, 'font-size':'14px', 'font-weight':'700', cursor:'pointer', opacity:(!newColName().trim() || isCreating()) ? '0.5' : '1' }}>
                {isCreating() ? '…' : `+ ${translate('collection_new')}`}
              </button>
            </div>

            <div style={{ display:'flex', 'flex-direction':'column', gap:'7px' }}>
              <For each={cols()}>{col => (
                <div onClick={() => openCollection(col)}
                  style={{ display:'flex', 'align-items':'center', gap:'11px', padding:'13px 15px', 'border-radius':'13px', background:activeCol()?.id === col.id ? 'rgba(69,123,157,0.15)' : 'var(--bg2)', border:`1px solid ${activeCol()?.id === col.id ? 'var(--accent)' : 'var(--border)'}`, cursor:'pointer', opacity: col.is_hidden ? '0.55' : '1' }}>
                  <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2">
                    <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
                  </svg>
                  <span style={{ flex:'1', 'font-family':sans, 'font-size':'14px', 'font-weight':'600', color:activeCol()?.id === col.id ? 'var(--accent-light)' : 'var(--text)', display:'flex', 'align-items':'center', gap:'6px' }}>
                    {col.name}
                    <Show when={col.is_hidden}>
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2" aria-label={translate('col_hidden')}>
                        <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/>
                      </svg>
                    </Show>
                  </span>
                  <span style={{ 'font-family':mono, 'font-size':'11px', color:'var(--muted)', background:'var(--bg3)', 'border-radius':'5px', padding:'2px 7px' }}>{col.design_count ?? 0}</span>
                  <button onClick={e => { e.stopPropagation(); setPendingDeleteCol(col) }}
                    style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', 'font-size':'17px', opacity:'0.5' }}>×</button>
                </div>
              )}</For>
              <Show when={cols().length === 0}>
                <div style={{ 'font-family':mono, 'font-size':'14px', color:'var(--muted)', 'text-align':'center', padding:'26px' }}>{translate('col_no_collections')}</div>
              </Show>
            </div>
          </div>

          {/* Content */}
          <div>
            <Show when={!activeCol()}>
              <div style={{ display:'flex', 'align-items':'center', 'justify-content':'center', height:'220px', color:'var(--muted)', 'font-family':mono, 'font-size':'15px' }}>{translate('col_select_hint')}</div>
            </Show>
            <Show when={activeCol()}>
              {/* Filter bar + result count on same line */}
              <div style={{ display:'flex', 'align-items':'center', gap:'11px', 'margin-bottom':'22px', 'flex-wrap':'wrap' }}>
                <Show when={renamingCol()} fallback={
                  <div style={{ display:'flex', 'align-items':'center', gap:'9px', 'flex-shrink':'0' }}>
                    <h2 onDblClick={startRename} title={translate('col_rename')}
                      style={{ 'font-family':sans, 'font-size':'20px', 'font-weight':'700', color:'var(--text)', cursor:'text' }}>{activeCol()!.name}</h2>
                    <button onClick={startRename} title={translate('col_rename')} aria-label={translate('col_rename')}
                      style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', padding:'2px', display:'flex', 'align-items':'center' }}>
                      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"/>
                      </svg>
                    </button>
                    {/* Hide / unhide: keeps the collection but removes it from the
                        filter and design details. Reversible from here. */}
                    <button onClick={() => toggleHidden(activeCol()!)}
                      title={translate(activeCol()!.is_hidden ? 'col_unhide' : 'col_hide')}
                      aria-label={translate(activeCol()!.is_hidden ? 'col_unhide' : 'col_hide')}
                      style={{ background:'none', border:'none', color:activeCol()!.is_hidden ? 'var(--accent-light)' : 'var(--muted)', cursor:'pointer', padding:'2px', display:'flex', 'align-items':'center' }}>
                      <Show when={activeCol()!.is_hidden} fallback={
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                      }>
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                      </Show>
                    </button>
                    {/* Says where the collection came from - the name alone no
                        longer does once it has been renamed here. */}
                    <Show when={activeCol()!.source_platform}>
                      <span title={translate('col_synced_from', {platform: platformLabel(activeCol()!.source_platform!, translate)})}
                        style={{ 'font-family':mono, 'font-size':'10px', 'font-weight':'700', color:'#fff', background:(PLATFORM_COLORS[activeCol()!.source_platform!] ?? 'rgba(0,0,0,0.45)'), 'border-radius':'5px', padding:'2px 7px', cursor:'help' }}>
                        {platformLabel(activeCol()!.source_platform!, translate)}
                      </span>
                    </Show>
                  </div>
                }>
                  <input value={renameDraft()} onInput={e => setRenameDraft(e.currentTarget.value)}
                    ref={element => queueMicrotask(() => { element.focus(); element.select() })}
                    onBlur={commitRename}
                    onKeyDown={(e: KeyboardEvent) => {
                      if (e.key === 'Enter') commitRename()
                      if (e.key === 'Escape') setRenamingCol(false)
                    }}
                    style={{ background:'var(--input-bg)', border:'1px solid var(--accent)', 'border-radius':'10px', padding:'6px 12px', color:'var(--text)', 'font-family':sans, 'font-size':'20px', 'font-weight':'700', outline:'none', 'flex-shrink':'0', width:'320px', 'max-width':'100%' }} />
                </Show>
                <div style={{ flex:'1', position:'relative', 'min-width':'160px' }}>
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2" style={{ position:'absolute', left:'11px', top:'50%', transform:'translateY(-50%)' }}>
                    <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
                  </svg>
                  <input value={colSearch()} onInput={e => setColSearch(e.currentTarget.value)}
                    placeholder={translate('col_search_placeholder')}
                    style={{ width:'100%', background:'var(--input-bg)', border:'1px solid var(--border2)', 'border-radius':'10px', padding:'8px 12px 8px 34px', color:'var(--text)', 'font-family':sans, 'font-size':'13px', outline:'none', 'box-sizing':'border-box' }} />
                </div>
                <button onClick={() => setShowColFilter(currentValue => !currentValue)}
                  style={{ background: colHasActiveFilters() ? 'rgba(69,123,157,0.18)' : 'var(--input-bg)', border:`1px solid ${colHasActiveFilters() ? 'var(--accent)' : 'var(--border2)'}`, 'border-radius':'10px', padding:'8px 14px', color: colHasActiveFilters() ? 'var(--accent-light)' : 'var(--text)', 'font-family':sans, 'font-size':'13px', cursor:'pointer', display:'flex', 'align-items':'center', gap:'7px', 'flex-shrink':'0' }}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><line x1="4" y1="6" x2="20" y2="6"/><line x1="8" y1="12" x2="16" y2="12"/><line x1="11" y1="18" x2="13" y2="18"/></svg>
                  {translate('filter_title')}
                  <Show when={colHasActiveFilters()}>
                    <span style={{ background:'var(--accent)', color:'#fff', 'border-radius':'50%', width:'16px', height:'16px', 'font-size':'10px', display:'flex', 'align-items':'center', 'justify-content':'center', 'font-weight':'700' }}>
                      {(colFilters().source_platform ? 1 : 0) + colFilters().tag_ids.length + (colFilters().shared_only ? 1 : 0) + (colFilters().show_hidden ? 1 : 0)}
                    </span>
                  </Show>
                </button>
                <button onClick={() => setShowAddDesigns(true)}
                  style={{ background:'var(--accent)', border:'none', 'border-radius':'10px', padding:'8px 14px', color:'#fff', 'font-family':sans, 'font-size':'13px', 'font-weight':'600', cursor:'pointer', display:'flex', 'align-items':'center', gap:'7px', 'flex-shrink':'0' }}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
                  {translate('col_add_designs')}
                </button>
              </div>
              <Show when={showColFilter()}>
                <FilterModal filters={colFilters()} setFilters={setColFilters} allTags={allTags()} onClose={() => setShowColFilter(false)} />
              </Show>
              <Show when={showAddDesigns() && activeCol()}>
                <AddDesignsToCollectionModal
                  collectionId={activeCol()!.id}
                  collectionName={activeCol()!.name}
                  onClose={() => setShowAddDesigns(false)}
                  onAdded={() => { openCollection(activeCol()!); refreshCols() }}
                  showToast={props.showToast} />
              </Show>
              {/* Result count inline with heading */}
              <span style={{ 'font-family':mono, 'font-size':'13px', color:'var(--muted)', 'margin-bottom':'10px', display:'block' }}>
                {translate(filteredColDesigns().length === 1 ? 'col_result' : 'col_results', {n: filteredColDesigns().length})}
              </span>

              <Show when={loadingDesigns()}><div style={{ color:'var(--muted)', 'font-family':mono }}>{translate('label_loading')}</div></Show>
              <Show when={!loadingDesigns() && filteredColDesigns().length === 0}>
                <div style={{ color:'var(--muted)', 'font-family':mono, 'font-size':'15px', 'text-align':'center', padding:'40px 0' }}>{colDesigns().length === 0 ? translate('collection_empty') : translate('col_no_matches')}</div>
              </Show>
              <div style={{ display:'flex', 'flex-wrap':'wrap', gap:'24px' }}>
                <For each={filteredColDesigns()}>{(colDesign, colIndex) =>
                  <DesignCard design={colDesign} index={colIndex()} onOpen={() => props.onOpenDesign(colDesign.id)} />
                }</For>
              </div>
            </Show>
          </div>
        </div>

        <Show when={pendingDeleteCol()}>
          <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.65)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'600', 'backdrop-filter':'blur(5px)' }}
            onClick={() => setPendingDeleteCol(null)}>
            <div onClick={e => e.stopPropagation()}
              style={{ background:'var(--bg2)', 'border-radius':'18px', padding:'28px 32px', width:'420px', border:'1px solid var(--border)', 'box-shadow':'0 24px 64px rgba(0,0,0,0.5)' }}>
              <div style={{ 'font-family':sans, 'font-size':'18px', 'font-weight':'700', color:'var(--text)', 'margin-bottom':'10px' }}>{translate('delete_collection_title')}</div>
              <div style={{ 'font-family':sans, 'font-size':'14px', color:'var(--text2)', 'margin-bottom':'24px', 'line-height':'1.6' }}>
                {translate('delete_collection_body', { name: pendingDeleteCol()!.name })}
              </div>
              <div style={{ display:'flex', gap:'11px', 'justify-content':'flex-end' }}>
                <button onClick={() => setPendingDeleteCol(null)}
                  style={{ padding:'9px 20px', background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'10px', color:'var(--muted)', 'font-size':'14px', cursor:'pointer', 'font-family':sans }}>
                  {translate('btn_cancel')}
                </button>
                <button onClick={() => { const col = pendingDeleteCol(); if (col) deleteCollection(col.id); setPendingDeleteCol(null) }}
                  style={{ padding:'9px 20px', background:'var(--danger-bg)', border:'1px solid var(--danger)', 'border-radius':'10px', color:'var(--danger)', 'font-size':'14px', 'font-weight':'700', cursor:'pointer', 'font-family':sans }}>
                  {translate('btn_confirm_delete')}
                </button>
              </div>
            </div>
          </div>
        </Show>
      </div>
    </div>
  )
}
