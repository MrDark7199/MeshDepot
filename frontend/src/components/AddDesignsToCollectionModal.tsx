import { PLATFORM_COLORS, platformLabel } from '../constants/platforms'
import { useI18n } from '../i18n/index'
import { useAuth } from '../services/AuthContext'
import { api } from '../services/api'
import { For, Show, createSignal } from 'solid-js'
import type { Design, DesignID } from '../types'
import { gradientFor } from '../utils/designDisplay'
import { displayAuthor, displayName } from '../utils/designText'

/**
 * Modal for adding designs to a collection. Lists the user's addable designs
 * (owned or shared, not yet in the collection) server-side with search + paging,
 * lets the user multi-select, and adds the chosen ones on confirm.
 */
export function AddDesignsToCollectionModal(props: {
  collectionId: number
  collectionName: string
  onClose: () => void
  onAdded: () => void
  showToast: (m: string, v?: string) => void
}) {
  const { translate, lang, translateDesigns } = useI18n()
  const { user } = useAuth()
  const PER_PAGE = 40
  const [search, setSearch] = createSignal('')
  const [items, setItems] = createSignal<Design[]>([])
  const [total, setTotal] = createSignal(0)
  const [loading, setLoading] = createSignal(false)
  const [adding, setAdding] = createSignal(false)
  const [selected, setSelected] = createSignal<Set<DesignID>>(new Set())
  let pageLoaded = 0
  let searchTimer: ReturnType<typeof setTimeout>

  const load = async (page: number, query: string, append: boolean) => {
    setLoading(true)
    try {
      const res: any = await api.getAddableDesigns(props.collectionId, query, page, PER_PAGE)
      const incoming: Design[] = res.data?.items || []
      setItems(prev => append ? [...prev, ...incoming] : incoming)
      setTotal(res.data?.total ?? 0)
      pageLoaded = page
    } catch {} finally { setLoading(false) }
  }
  load(1, '', false)

  const onSearch = (q: string) => {
    setSearch(q)
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => load(1, q.trim(), false), 300)
  }

  const toggle = (id: DesignID) => setSelected(prev => {
    const next = new Set(prev)
    next.has(id) ? next.delete(id) : next.add(id)
    return next
  })

  const confirm = async () => {
    if (selected().size === 0) return
    setAdding(true)
    try {
      await api.addToCollection(props.collectionId, [...selected()])
      props.showToast(translate('col_add_done', { n: selected().size }))
      props.onAdded()
      props.onClose()
    } catch { props.showToast(translate('col_add_failed'), 'error') }
    finally { setAdding(false) }
  }

  const hasMore = () => items().length < total()
  const sans = "'DM Sans',sans-serif"
  const mono = "'DM Mono',monospace"

  return (
    <div onClick={props.onClose}
      style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.8)', display:'flex', 'align-items':'center', 'justify-content':'center', 'z-index':'2000', 'backdrop-filter':'blur(6px)' }}>
      <div onClick={e => e.stopPropagation()}
        style={{ background:'var(--bg)', 'border-radius':'18px', width:'560px', 'max-width':'94vw', height:'min(86vh, 720px)', display:'flex', 'flex-direction':'column', overflow:'hidden', border:'1px solid var(--border)', 'box-shadow':'0 40px 100px rgba(0,0,0,0.5)' }}>
        {/* Header */}
        <div style={{ padding:'18px 22px', 'border-bottom':'1px solid var(--border)', display:'flex', 'align-items':'center', 'justify-content':'space-between', gap:'12px', 'flex-shrink':'0' }}>
          <span style={{ 'font-family':sans, 'font-weight':'700', 'font-size':'16px', color:'var(--text)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
            {translate('col_add_title', { name: props.collectionName })}
          </span>
          <button onClick={props.onClose} style={{ background:'none', border:'none', color:'var(--muted)', cursor:'pointer', 'font-size':'22px', 'line-height':'1', 'flex-shrink':'0' }}>×</button>
        </div>
        {/* Search */}
        <div style={{ padding:'14px 22px 10px', 'flex-shrink':'0' }}>
          <div style={{ position:'relative' }}>
            <input value={search()} onInput={e => onSearch(e.currentTarget.value)} placeholder={translate('col_add_search')}
              style={{ width:'100%', background:'var(--input-bg)', border:'1px solid var(--border2)', 'border-radius':'10px', padding:`10px ${search() ? '38px' : '14px'} 10px 14px`, color:'var(--text)', 'font-family':sans, 'font-size':'14px', outline:'none', 'box-sizing':'border-box' }} />
            <Show when={search()}>
              <button onClick={() => { clearTimeout(searchTimer); setSearch(''); load(1, '', false) }}
                style={{ position:'absolute', right:'11px', top:'50%', transform:'translateY(-50%)', background:'none', border:'none', cursor:'pointer', display:'flex', 'align-items':'center', padding:'2px', color:'var(--muted)' }}>
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
              </button>
            </Show>
          </div>
        </div>
        {/* List */}
        <div style={{ flex:'1', 'overflow-y':'auto', padding:'0 22px' }}>
          <Show when={!loading() && items().length === 0}>
            <div style={{ color:'var(--muted)', 'font-family':mono, 'font-size':'14px', 'text-align':'center', padding:'40px 0' }}>{translate('col_add_empty')}</div>
          </Show>
          <For each={items()}>{design => {
            const isSel = () => selected().has(design.id)
            const cover = () => design.cover_path ? api.coverUrl(design.cover_path) : null
            const authorName = () => displayAuthor(design, user()?.name)
            return (
              <div onClick={() => toggle(design.id)}
                style={{ display:'flex', 'align-items':'center', gap:'12px', padding:'9px 10px', 'border-radius':'10px', cursor:'pointer', border:`1px solid ${isSel() ? 'var(--accent)' : 'transparent'}`, background:isSel() ? 'rgba(69,123,157,0.12)' : 'transparent', 'margin-bottom':'4px' }}>
                <div style={{ width:'42px', height:'42px', 'flex-shrink':'0', 'border-radius':'8px', overflow:'hidden', background:`linear-gradient(135deg,${gradientFor(design.id)[0]},${gradientFor(design.id)[1]})`, position:'relative' }}>
                  <Show when={cover()}>
                    <img src={cover()!} alt="" style={{ width:'100%', height:'100%', 'object-fit':'cover' }} onError={e => { (e.target as HTMLImageElement).style.display='none' }} />
                  </Show>
                </div>
                <div style={{ flex:'1', 'min-width':'0' }}>
                  <div style={{ 'font-family':sans, 'font-size':'14px', 'font-weight':'600', color:'var(--text)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>
                    {displayName(design, lang(), translateDesigns())}
                  </div>
                  <div style={{ display:'flex', 'align-items':'center', gap:'7px', 'margin-top':'2px' }}>
                    <Show when={design.source_platform}>
                      <span style={{ 'font-family':mono, 'font-size':'10px', 'font-weight':'700', color:'#fff', background:(PLATFORM_COLORS[design.source_platform!] ?? 'rgba(0,0,0,0.45)'), 'border-radius':'5px', padding:'1px 6px' }}>
                        {platformLabel(design.source_platform!, translate)}
                      </span>
                    </Show>
                    <Show when={design.is_shared}>
                      <span style={{ 'font-family':mono, 'font-size':'10px', 'font-weight':'700', color:'#fff', background:'rgba(69,123,157,0.85)', 'border-radius':'5px', padding:'1px 6px' }}>shared</span>
                    </Show>
                    <Show when={authorName()}>
                      <span style={{ 'font-family':mono, 'font-size':'11px', color:'var(--muted)', overflow:'hidden', 'text-overflow':'ellipsis', 'white-space':'nowrap' }}>{authorName()}</span>
                    </Show>
                  </div>
                </div>
                <div style={{ width:'22px', height:'22px', 'flex-shrink':'0', 'border-radius':'6px', border:`2px solid ${isSel() ? 'var(--accent)' : 'var(--border2)'}`, background:isSel() ? 'var(--accent)' : 'transparent', display:'flex', 'align-items':'center', 'justify-content':'center', color:'#fff', 'font-size':'13px' }}>
                  <Show when={isSel()}>✓</Show>
                </div>
              </div>
            )
          }}</For>
          <Show when={hasMore() && !loading()}>
            <button onClick={() => load(pageLoaded + 1, search().trim(), true)}
              style={{ width:'100%', background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'10px', padding:'9px 0', 'font-family':sans, 'font-size':'13px', color:'var(--text2)', cursor:'pointer', margin:'8px 0 14px' }}>
              {translate('col_add_load_more')}
            </button>
          </Show>
          <Show when={loading()}>
            <div style={{ color:'var(--muted)', 'font-family':mono, 'font-size':'13px', 'text-align':'center', padding:'14px 0' }}>{translate('label_loading')}</div>
          </Show>
        </div>
        {/* Footer */}
        <div style={{ padding:'14px 22px', 'border-top':'1px solid var(--border)', display:'flex', 'align-items':'center', 'justify-content':'space-between', gap:'12px', 'flex-shrink':'0' }}>
          <span style={{ 'font-family':mono, 'font-size':'12px', color:'var(--muted)' }}>{translate('col_add_total', { n: total() })}</span>
          <div style={{ display:'flex', gap:'9px' }}>
            <button onClick={props.onClose} style={{ background:'transparent', border:'1px solid var(--border)', 'border-radius':'9px', padding:'9px 16px', 'font-family':sans, 'font-size':'13px', color:'var(--text2)', cursor:'pointer' }}>{translate('btn_cancel')}</button>
            <button onClick={confirm} disabled={selected().size === 0 || adding()}
              style={{ background:selected().size === 0 ? 'var(--surface)' : 'var(--accent)', border:'none', 'border-radius':'9px', padding:'9px 18px', 'font-family':sans, 'font-size':'13px', 'font-weight':'600', color:selected().size === 0 ? 'var(--muted)' : '#fff', cursor:selected().size === 0 ? 'default' : 'pointer', opacity:adding() ? '0.7' : '1' }}>
              {adding() ? '…' : translate('col_add_confirm', { n: selected().size })}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
