import { PLATFORM_LABELS, platformLabel } from '../constants/platforms'
import { useI18n } from '../i18n/index'
import { api } from '../services/api'
import { For, Show, createSignal, onMount, on } from 'solid-js'
import type { CustomField, Filters, Tag } from '../types'

export function FilterModal(props: {filters: Filters; setFilters: (f: Filters) => void; allTags: Tag[]; onClose: () => void; perPage?: number; onPerPageChange?: (pp: number) => void}) {
  const { translate } = useI18n()
  const [local, setLocal] = createSignal({...props.filters})
  const [localPerPage, setLocalPerPage] = createSignal(props.perPage ?? 50)
  // Only the reader's own fields, and only shown when there are any - a library
  // without custom fields keeps the filter as it was.
  const [customFields, setCustomFields] = createSignal<CustomField[]>([])
  onMount(async () => {
    try {
      const answer = await api.customFields() as any
      setCustomFields(answer?.data ?? [])
    } catch (_) { setCustomFields([]) }
  })
  const customValue = (fieldId: number) => local().custom?.[String(fieldId)] ?? ''
  const setCustomValue = (fieldId: number, value: string) =>
    setLocal(current => ({ ...current, custom: { ...(current.custom ?? {}), [String(fieldId)]: value } }))
  const lbl: any = { display:'block', 'font-family':"'DM Mono',monospace", 'font-size':'12px', color:'var(--muted)', 'text-transform':'uppercase', 'letter-spacing':'0.05em', 'margin-bottom':'8px' }
  const inp: any = { width:'100%', background:'var(--input-bg)', border:'1px solid var(--border2)', 'border-radius':'10px', padding:'10px 14px', color:'var(--text)', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', outline:'none' }
  // Tag filter as a select2-style combobox: matches are searched on the server
  // rather than by loading every tag up front. Picks are held as chips, and the
  // initial selection is resolved from the allTags list that is loaded anyway.
  const [selectedTags, setSelectedTags] = createSignal<Tag[]>(props.allTags.filter(t => props.filters.tag_ids?.includes(t.id)))
  const [tagQuery, setTagQuery] = createSignal('')
  const [tagResults, setTagResults] = createSignal<Tag[]>([])
  const [tagOpen, setTagOpen] = createSignal(false)
  const notSelected = (t: Tag) => !selectedTags().some(s => s.id === t.id)
  const runTagSearch = async (v: string) => {
    try { const r = await api.searchTags(v.trim()) as { data: Tag[] }; setTagResults(r.data || []) }
    catch { setTagResults([]) }
  }
  let tagSearchTimer: ReturnType<typeof setTimeout>
  const onTagInput = (v: string) => { setTagQuery(v); setTagOpen(true); clearTimeout(tagSearchTimer); tagSearchTimer = setTimeout(() => runTagSearch(v), 200) }
  const addTag = (t: Tag) => { if (notSelected(t)) setSelectedTags(ts => [...ts, t]); setTagQuery(''); setTagResults([]); setTagOpen(false) }
  const removeTag = (id: number) => setSelectedTags(ts => ts.filter(t => t.id !== id))
  const onTagKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter') { e.preventDefault(); const a = tagResults().filter(notSelected); if (a.length) addTag(a[0]) }
    else if (e.key === 'Escape') setTagOpen(false)
  }
  return (
    <div style={{ position:'fixed', inset:'0', background:'rgba(0,0,0,0.5)', display:'flex', 'align-items':'flex-start', 'justify-content':'center', 'z-index':'500', 'padding-top':'96px' }}
      onClick={props.onClose}>
      <div onClick={e => e.stopPropagation()}
        style={{ background:'var(--bg2)', 'border-radius':'20px', padding:'30px', width:'420px', border:'1px solid var(--border)', 'box-shadow':'0 20px 60px rgba(0,0,0,0.4)' }}>
        <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', 'font-size':'18px', color:'var(--text)', 'margin-bottom':'22px' }}>{translate('filter_title')}</div>
        <div style={{ display:'flex', 'flex-direction':'column', gap:'20px' }}>
          <Show when={props.onPerPageChange}>
            <div>
              <label style={lbl}>Einträge pro Seite</label>
              <div style={{ display:'flex', gap:'8px', 'flex-wrap':'wrap' }}>
                <For each={[5, 50, 100, 500, 1000]}>{n => (
                  <button onClick={() => setLocalPerPage(n)}
                    style={{ padding:'7px 16px', 'border-radius':'9px', border:`1px solid ${localPerPage()===n?'var(--accent)':'var(--border)'}`, background:localPerPage()===n?'rgba(69,123,157,0.15)':'var(--surface)', color:localPerPage()===n?'var(--accent-light)':'var(--text2)', 'font-family':"'DM Mono',monospace", 'font-size':'13px', cursor:'pointer' }}>
                    {n}
                  </button>
                )}</For>
              </div>
            </div>
          </Show>
          <div>
            <label style={lbl}>{translate('filter_platform')}</label>
            <select value={local().source_platform} onChange={e => setLocal(l => ({...l, source_platform: e.currentTarget.value}))} style={inp}>
              <option value="">{translate('filter_all_platforms')}</option>
              <For each={Object.entries(PLATFORM_LABELS)}>{([k]) => <option value={k}>{platformLabel(k, translate)}</option>}</For>
            </select>
          </div>
          <div>
            <label style={lbl}>{translate('filter_visibility')}</label>
            <div style={{ display:'flex', gap:'8px', 'flex-wrap':'wrap' }}>
              <button onClick={() => setLocal(l => ({...l, shared_only: !l.shared_only}))}
                style={{ padding:'9px 17px', 'border-radius':'9px', border:`1px solid ${local().shared_only ? 'var(--accent)' : 'var(--border)'}`, background:local().shared_only ? 'rgba(69,123,157,0.15)' : 'var(--surface)', color:local().shared_only ? 'var(--accent-light)' : 'var(--muted)', 'font-size':'14px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-weight':'500' }}>
                {translate('filter_shared_only')}
              </button>
              <button onClick={() => setLocal(l => ({...l, show_hidden: !l.show_hidden}))}
                style={{ padding:'9px 17px', 'border-radius':'9px', border:`1px solid ${local().show_hidden ? 'var(--accent)' : 'var(--border)'}`, background:local().show_hidden ? 'rgba(69,123,157,0.15)' : 'var(--surface)', color:local().show_hidden ? 'var(--accent-light)' : 'var(--muted)', 'font-size':'14px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-weight':'500' }}>
                {translate('filter_show_hidden')}
              </button>
            </div>
          </div>
          <div>
            <label style={lbl}>{translate('filter_tags')}</label>
            {/* Ausgewählte Tags als entfernbare Chips */}
            <Show when={selectedTags().length > 0}>
              <div style={{ display:'flex', 'flex-wrap':'wrap', gap:'7px', 'margin-bottom':'8px' }}>
                <For each={selectedTags()}>{tag => (
                  <span style={{ display:'inline-flex', 'align-items':'center', gap:'6px', 'font-size':'13px', padding:'4px 6px 4px 11px', 'border-radius':'8px', background:tag.color+'33', color:tag.color, 'font-family':"'DM Sans',sans-serif", 'font-weight':'500' }}>
                    {tag.name}
                    <button onClick={() => removeTag(tag.id)} style={{ background:'none', border:'none', color:tag.color, cursor:'pointer', 'font-size':'15px', 'line-height':'1', padding:'0' }}>×</button>
                  </span>
                )}</For>
              </div>
            </Show>
            {/* Sucheingabe + Dropdown (serverseitige Suche) */}
            <div style={{ position:'relative' }}>
              <input value={tagQuery()} placeholder={translate('filter_tag_search_placeholder')}
                onInput={e => onTagInput(e.currentTarget.value)}
                onFocus={() => { setTagOpen(true); runTagSearch(tagQuery()) }}
                onClick={() => { if (!tagOpen()) { setTagOpen(true); runTagSearch(tagQuery()) } }}
                onBlur={() => setTimeout(() => setTagOpen(false), 150)}
                onKeyDown={onTagKeyDown}
                style={{ ...inp, 'font-size':'13px', padding:'8px 12px' }} />
              <Show when={tagOpen() && tagResults().filter(notSelected).length > 0}>
                <div style={{ position:'absolute', top:'calc(100% + 4px)', left:'0', right:'0', 'z-index':'40', background:'var(--bg2)', border:'1px solid var(--border)', 'border-radius':'10px', 'box-shadow':'0 12px 30px rgba(0,0,0,0.35)', 'max-height':'200px', 'overflow-y':'auto', padding:'5px' }}>
                  <For each={tagResults().filter(notSelected)}>{tag => (
                    <button onMouseDown={e => e.preventDefault()} onClick={() => addTag(tag)}
                      style={{ display:'flex', 'align-items':'center', gap:'8px', width:'100%', 'text-align':'left', padding:'7px 9px', background:'none', border:'none', 'border-radius':'7px', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-size':'13px', color:'var(--text2)' }}>
                      <span style={{ width:'10px', height:'10px', 'border-radius':'3px', background:tag.color, 'flex-shrink':'0' }} />
                      {tag.name}
                    </button>
                  )}</For>
                </div>
              </Show>
            </div>
          </div>
        </div>
        <Show when={customFields().length > 0}>
          <div style={{ 'margin-top':'20px', 'border-top':'1px solid var(--border)', 'padding-top':'18px' }}>
            <label style={lbl}>{translate('section_custom_fields')}</label>
            <div style={{ display:'grid', 'grid-template-columns':'1fr 1fr', gap:'12px' }}>
              <For each={customFields()}>{field => (
                <div>
                  <label style={{ ...lbl, 'font-size':'11px', 'margin-bottom':'5px' }}>{field.name}</label>
                  {/* A multiple choice filters on one of its entries, so it offers the
                      same list as a single choice does. A free field says what it
                      expects instead of standing there blank. */}
                  <Show when={field.field_type === 'select' || field.field_type === 'multiselect' || field.field_type === 'boolean'} fallback={
                    <input style={inp} value={customValue(field.id)}
                      placeholder={translate(`custom_field_type_${field.field_type}` as any)}
                      onInput={event => setCustomValue(field.id, event.currentTarget.value)} />
                  }>
                    <select style={inp} value={customValue(field.id)}
                      onChange={event => setCustomValue(field.id, event.currentTarget.value)}>
                      <option value="">{translate('filter_all')}</option>
                      <Show when={field.field_type === 'boolean'} fallback={
                        <For each={field.options ?? []}>{choice => <option value={choice}>{choice}</option>}</For>
                      }>
                        <option value="1">{translate('label_yes')}</option>
                        <option value="0">{translate('label_no')}</option>
                      </Show>
                    </select>
                  </Show>
                </div>
              )}</For>
            </div>
          </div>
        </Show>
        <div style={{ display:'flex', gap:'11px', 'margin-top':'24px' }}>
          <button onClick={() => { props.setFilters({source_platform:'',tag_ids:[],shared_only:false,show_hidden:false,custom:{}}); setLocal({source_platform:'',tag_ids:[],shared_only:false,show_hidden:false,custom:{}}); setSelectedTags([]); props.onPerPageChange?.(50); setLocalPerPage(50); props.onClose() }}
            style={{ flex:'1', padding:'11px', background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'10px', color:'var(--muted)', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px' }}>
            {translate('filter_clear')}
          </button>
          <button onClick={() => { props.setFilters({ ...local(), tag_ids: selectedTags().map(t => t.id) }); props.onPerPageChange?.(localPerPage()); props.onClose() }}
            style={{ flex:'2', padding:'11px', background:'var(--accent)', border:'none', 'border-radius':'10px', color:'#fff', cursor:'pointer', 'font-family':"'DM Sans',sans-serif", 'font-size':'14px', 'font-weight':'600' }}>
            {translate('filter_apply')}
          </button>
        </div>
      </div>
    </div>
  )
}
