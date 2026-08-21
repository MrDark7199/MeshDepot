import { createSignal, createEffect, onCleanup, Show, For, JSX } from 'solid-js'
import { api } from '../services/api'
import type { User, QueueBlock } from '../types'
import { useI18n } from '../i18n/index'
import { useUnsavedChanges, resetDirty, markDirty } from '../utils/unsavedChanges'
import { errorKey } from '../utils/errorMessage'
import { formatDate, formatDateTime } from '../utils/datetime'
import { PLATFORM_COLORS } from '../constants/platforms'

const inp: JSX.CSSProperties = { width: '100%', background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '9px', padding: '9px 12px', color: 'var(--text)', 'font-family': "'DM Sans', sans-serif", 'font-size': '13px', outline: 'none', 'box-sizing': 'border-box' }
const lbl: JSX.CSSProperties = { display: 'block', 'font-family': "'DM Mono', monospace", 'font-size': '11px', color: 'var(--muted)', 'margin-bottom': '5px', 'letter-spacing': '0.05em', 'text-transform': 'uppercase' }
const mono: JSX.CSSProperties = { 'font-family': "'DM Mono', monospace" }
const sans: JSX.CSSProperties = { 'font-family': "'DM Sans', sans-serif" }

function fmtBytes(b: number): string {
  if (!b) return '0 B'
  if (b >= 1073741824) return (b/1073741824).toFixed(1)+' GB'
  if (b >= 1048576)    return (b/1048576).toFixed(1)+' MB'
  return (b/1024).toFixed(0)+' KB'
}

function Err(props: { message: string }) { return <Show when={props.message}><div style={{ background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', padding: '8px 12px', color: 'var(--danger)', 'font-size': '13px' }}>{props.message}</div></Show> }
function Ok(props: { message: string })  { return <Show when={props.message}><div style={{ background: 'var(--success-bg)', border: '1px solid var(--success-border)', 'border-radius': '8px', padding: '8px 12px', color: 'var(--success)', 'font-size': '13px' }}>{props.message}</div></Show> }

function Card(props: { children: JSX.Element; style?: Record<string, string> }) {
  return <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '20px', display: 'flex', 'flex-direction': 'column', gap: '14px', ...(props.style || {}) }}>{props.children}</div>
}
/**
 * A settings block that starts collapsed and shows its state on the right.
 *
 * The three sync blocks are long enough that the page only showed the first one
 * without scrolling; collapsed they fit on screen together, so what exists is
 * visible before anything is opened.
 */
/**
 * `statusOff` marks the state as "switched off" rather than merely "not ok":
 * the neutral grey badge of a disabled sync was hard to spot next to the green
 * of an active one, which is the difference an admin is looking for. A block
 * with no on/off state at all (the cooldowns) leaves `status` out - a badge
 * that only ever reads "settings" states what the card already says.
 */
function CollapsibleCard(props: { label: string; status?: string; statusOk?: boolean; statusOff?: boolean; children: JSX.Element }) {
  const [open, setOpen] = createSignal(false)
  const badgeBackground = () => props.statusOk ? 'rgba(82,183,136,0.15)' : props.statusOff ? 'var(--danger-bg)' : 'var(--bg4)'
  const badgeColor = () => props.statusOk ? 'var(--success)' : props.statusOff ? 'var(--danger)' : 'var(--muted)'
  return (
    <div style={{ background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '12px', padding: open() ? '20px' : '14px 20px', display: 'flex', 'flex-direction': 'column', gap: open() ? '14px' : '0' }}>
      <button onClick={() => setOpen(value => !value)}
        style={{ display: 'flex', 'align-items': 'center', gap: '12px', width: '100%', background: 'none', border: 'none', padding: '0', cursor: 'pointer', 'text-align': 'left' }}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="3"
          style={{ transform: open() ? 'rotate(90deg)' : 'none', transition: 'transform 0.15s', 'flex-shrink': '0' }}>
          <polyline points="9 18 15 12 9 6" />
        </svg>
        <span style={{ ...sans, 'font-size': '11px', 'font-weight': '700', color: 'var(--muted)', 'letter-spacing': '0.1em', 'text-transform': 'uppercase', flex: '1' }}>{props.label}</span>
        <Show when={props.status}>
          <span style={{ ...mono, 'font-size': '10px', padding: '2px 8px', 'border-radius': '5px', 'flex-shrink': '0',
            border: `1px solid ${props.statusOff ? 'var(--danger-border)' : 'transparent'}`, 'font-weight': '700',
            background: badgeBackground(), color: badgeColor() }}>
            {props.status}
          </span>
        </Show>
      </button>
      <Show when={open()}>
        <div style={{ display: 'flex', 'flex-direction': 'column', gap: '14px', 'padding-top': '14px', 'border-top': '1px solid var(--border)' }}>
          {props.children}
        </div>
      </Show>
    </div>
  )
}

function SectionTitle(props: { label: string }) {
  return <div style={{ ...sans, 'font-size': '11px', 'font-weight': '700', color: 'var(--muted)', 'letter-spacing': '0.1em', 'text-transform': 'uppercase', 'padding-bottom': '8px', 'border-bottom': '1px solid var(--border)', 'margin-bottom': '4px' }}>{props.label}</div>
}
function Btn(props: { onClick: () => void; loading?: boolean; label: string; danger?: boolean; secondary?: boolean; disabled?: boolean; small?: boolean }) {
  const bg = () => props.danger ? 'var(--danger-bg)' : props.secondary ? 'var(--surface)' : (props.loading || props.disabled) ? 'var(--bg4)' : 'var(--accent)'
  const bd = () => props.danger ? '1px solid var(--danger-border)' : props.secondary ? '1px solid var(--border2)' : 'none'
  const co = () => props.danger ? 'var(--danger)' : props.secondary ? 'var(--text3)' : '#fff'
  return (
    <button onClick={props.onClick} disabled={props.loading || props.disabled}
      style={{ padding: props.small ? '6px 14px' : '10px 20px', background: bg(), border: bd(), 'border-radius': '9px', color: co(), ...sans, 'font-size': props.small ? '12px' : '13px', 'font-weight': '700', cursor: (props.loading || props.disabled) ? 'not-allowed' : 'pointer', opacity: (props.disabled && !props.loading) ? '0.5' : '1' }}>
      {props.loading ? '…' : props.label}
    </button>
  )
}
/**
 * Pill-style toggle switch with an optional text label.
 *
 * Every toggle here belongs to a form that is saved by a button, and its click
 * fires on a div - no input/change event the form's container could pick up -
 * so the unsaved-changes guard is marked from here.
 */
function Toggle(props: { value: boolean; onChange: (newValue: boolean) => void; label?: string }) {
  const flip = () => { props.onChange(!props.value); markDirty() }
  return (
    <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', cursor: 'pointer' }} onClick={flip}>
      <div style={{ width: '36px', height: '20px', 'border-radius': '10px', background: props.value ? 'var(--accent)' : 'var(--border2)', position: 'relative', transition: 'background 0.2s', 'flex-shrink': '0' }}>
        <div style={{ width: '16px', height: '16px', 'border-radius': '50%', background: '#fff', position: 'absolute', top: '2px', left: props.value ? '18px' : '2px', transition: 'left 0.2s', 'box-shadow': '0 1px 4px rgba(0,0,0,0.3)' }} />
      </div>
      <Show when={props.label}><span style={{ 'font-size': '13px', color: 'var(--text2)', ...sans }}>{props.label}</span></Show>
    </div>
  )
}
function LetterAvatar(props: { name: string; size?: number; fontSize?: number }) {
  const size = () => props.size ?? 32
  const fs = () => props.fontSize ?? 13
  const initials = () => (props.name || '?').split(' ').map((w: string) => w[0]).join('').toUpperCase().slice(0, 2)
  const colors = ['#e63946','#2a9d8f','#e9c46a','#f4a261','#457b9d','#a8dadc']
  const color = () => colors[(props.name || '?').charCodeAt(0) % colors.length]
  return <div style={{ width: `${size()}px`, height: `${size()}px`, 'border-radius': '50%', background: color(), display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': `${fs()}px`, 'font-weight': '700', color: '#fff', ...mono, 'flex-shrink': '0', 'user-select': 'none' }}>{initials()}</div>
}
function StatBox(props: { label: string; value: any; sub?: string }) {
  return (
    <div style={{ background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '12px', padding: '14px 18px', flex: '1', 'min-width': '110px' }}>
      <div style={{ ...mono, 'font-size': '22px', 'font-weight': '700', color: 'var(--accent-light)' }}>{props.value}</div>
      <div style={{ ...sans, 'font-size': '12px', color: 'var(--text3)', 'margin-top': '2px' }}>{props.label}</div>
      <Show when={props.sub}><div style={{ ...mono, 'font-size': '10px', color: 'var(--muted)', 'margin-top': '2px' }}>{props.sub}</div></Show>
    </div>
  )
}
function Spinner() {
  return <div style={{ width: '24px', height: '24px', border: '2px solid var(--border)', 'border-top': '2px solid var(--accent)', 'border-radius': '50%', animation: 'spin 0.8s linear infinite' }} />
}
function FieldView(props: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div style={{ ...lbl, 'margin-bottom': '3px' }}>{props.label}</div>
      <div style={{ 'font-size': '13px', color: 'var(--text2)', ...(props.mono ? mono : sans) }}>{props.value || '-'}</div>
    </div>
  )
}

/**
 * Admin stats tab - shows summary counts, disk usage bar,
 * designs-by-platform breakdown, and per-user storage bars.
 */
function TabStats() {
  const { translate, lang } = useI18n()
  const [data, setData] = createSignal<any>(null)
  const [loading, setLoading] = createSignal(true)
  const [err, setErr] = createSignal('')

  api.adminStats().then((r: any) => setData(r.data)).catch((failure: unknown) => setErr(translate(errorKey(failure)))).finally(() => setLoading(false))

  const diskUsed = () => (data()?.disk_total ?? 0) - (data()?.disk_free ?? 0)
  const diskPct = () => data()?.disk_total > 0 ? Math.round((diskUsed() / data().disk_total) * 100) : 0
  const maxUserBytes = () => Math.max(1, ...(data()?.per_user ?? []).map((u: any) => parseInt(u.used_bytes || '0')))

  return (
    <Show when={!loading()} fallback={<Spinner />}>
      <Show when={!err()} fallback={<Err message={err()} />}>
        <Show when={data()} fallback={<Err message={translate('admin_no_data')} />}>
          <div style={{ display: 'flex', 'flex-direction': 'column', gap: '20px' }}>

            {/* Summary row */}
            <div style={{ display: 'grid', 'grid-template-columns': 'repeat(4, 1fr)', gap: '10px' }}>
              <StatBox label={translate('admin_stat_users')} value={data().user_count ?? 0} sub={translate('admin_stat_active').replace('{count}', String(data().active_users ?? 0))} />
              <StatBox label={translate('admin_stat_designs')} value={data().design_count ?? 0} sub={translate('admin_stat_synced').replace('{count}', String(data().synced_count ?? 0))} />
              <StatBox label={translate('admin_stat_files')} value={data().file_count ?? 0} />
              <StatBox label={translate('admin_stat_storage')} value={fmtBytes(data().total_bytes ?? 0)} sub={translate('admin_stat_tags_collections').replace('{tags}', String(data().tag_count ?? 0)).replace('{collections}', String(data().collection_count ?? 0))} />
            </div>

            {/* Disk space */}
            <Show when={(data()?.disk_total ?? 0) > 0}>
              <Card>
                <SectionTitle label={translate('admin_disk_space')} />
                <div style={{ display: 'flex', 'justify-content': 'space-between', 'margin-bottom': '6px' }}>
                  <span style={{ ...sans, 'font-size': '12px', color: 'var(--text3)' }}>{translate('admin_disk_used') + ': '}{fmtBytes(diskUsed())}</span>
                  <span style={{ ...mono, 'font-size': '11px', color: 'var(--muted)' }}>{fmtBytes(data().disk_free)} {translate('admin_disk_free')} / {fmtBytes(data().disk_total)} {translate('admin_disk_total')}</span>
                </div>
                <div style={{ height: '8px', 'border-radius': '4px', background: 'var(--border)', overflow: 'hidden' }}>
                  <div style={{ height: '100%', width: `${diskPct()}%`, background: diskPct() > 90 ? 'var(--danger)' : diskPct() > 75 ? '#e9c46a' : 'var(--accent)', 'border-radius': '4px', transition: 'width 0.5s' }} />
                </div>
                <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'text-align': 'right', 'margin-top': '4px' }}>{`${diskPct()}${translate('admin_disk_pct_used')}`}</div>
              </Card>
            </Show>

            {/* Platform breakdown */}
            <Show when={(data()?.platforms ?? []).length > 0}>
              <Card>
                <SectionTitle label={translate('admin_designs_by_platform')} />
                <For each={data().platforms}>{(platform: any) => {
                  const pct = data().design_count > 0 ? Math.round((platform.cnt / data().design_count) * 100) : 0
                  const color = PLATFORM_COLORS[platform.platform] ?? 'var(--accent)'
                  return (
                    <div style={{ display: 'flex', 'align-items': 'center', gap: '10px' }}>
                      <span style={{ ...mono, 'font-size': '11px', color: 'var(--text3)', width: '90px', 'text-transform': 'capitalize', 'flex-shrink': '0' }}>{platform.platform}</span>
                      <div style={{ flex: '1', height: '6px', 'border-radius': '3px', background: 'var(--border)', overflow: 'hidden' }}>
                        <div style={{ height: '100%', width: `${pct}%`, background: color, 'border-radius': '3px' }} />
                      </div>
                      <span style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', width: '28px', 'text-align': 'right', 'flex-shrink': '0' }}>{platform.cnt}</span>
                    </div>
                  )
                }}</For>
              </Card>
            </Show>

            {/* Per-user breakdown */}
            <Show when={(data()?.per_user ?? []).length > 0}>
              <Card>
                <SectionTitle label={translate('admin_storage_per_user')} />
                <div style={{ 'max-height': '220px', 'overflow-y': 'auto', display: 'flex', 'flex-direction': 'column', gap: '8px', 'padding-right': '4px' }}>
                  <For each={data().per_user}>{(u: any) => {
                    const bytes = parseInt(u.used_bytes || '0')
                    const pct = Math.round((bytes / maxUserBytes()) * 100)
                    return (
                      <div style={{ display: 'flex', 'align-items': 'center', gap: '10px' }}>
                        <LetterAvatar name={u.name} size={24} fontSize={10} />
                        <span style={{ ...sans, 'font-size': '12px', color: 'var(--text2)', 'min-width': '100px', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>{u.name}</span>
                        <div style={{ flex: '1', height: '6px', 'border-radius': '3px', background: 'var(--border)', overflow: 'hidden' }}>
                          <div style={{ height: '100%', width: `${pct}%`, background: 'var(--accent)', 'border-radius': '3px' }} />
                        </div>
                        <span style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', width: '55px', 'text-align': 'right', 'flex-shrink': '0' }}>{fmtBytes(bytes)}</span>
                        <span style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', width: '60px', 'text-align': 'right', 'flex-shrink': '0' }}>{translate('admin_user_designs').replace('{count}', String(u.design_count ?? 0))}</span>
                      </div>
                    )
                  }}</For>
                </div>
              </Card>
            </Show>

            <Show when={data()?.newest_design_at}>
              <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'text-align': 'right' }}>{translate('admin_latest_design') + ': '}{formatDateTime(data().newest_design_at, lang())}</div>
            </Show>
          </div>
        </Show>
      </Show>
    </Show>
  )
}

function UserRow(props: { user: any; adminCount: number; onDetail: () => void; onToggle: () => void; showToast: (m: string, v?: string) => void }) {
  const { translate } = useI18n()
  const [loading, setLoading] = createSignal(false)
  const active = () => props.user.state === 'active'
  // Deactivating the last remaining active admin is rejected by the backend,
  // so hide the disable button entirely for that user.
  const isLastActiveAdmin = () => props.user.admin && active() && props.adminCount <= 1

  const toggle = async () => {
    setLoading(true)
    try { await api.adminUpdateUser(props.user.id, { state: active() ? 'inactive' : 'active' }); props.showToast(translate(active() ? 'toast_user_deactivated' : 'toast_user_activated')); props.onToggle() }
    catch (failure: unknown) { props.showToast(translate(errorKey(failure)), 'error') }
    finally { setLoading(false) }
  }

  return (
    <div style={{ display: 'flex', 'align-items': 'center', gap: '12px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', padding: '10px 14px' }}>
      <LetterAvatar name={props.user.name} />
      <div style={{ flex: '1', 'min-width': '0' }}>
        <div style={{ display: 'flex', 'align-items': 'center', gap: '8px', 'flex-wrap': 'wrap' }}>
          <span style={{ 'font-size': '13px', 'font-weight': '600', color: 'var(--text)', ...sans }}>{props.user.name}</span>
          <Show when={props.user.admin}><span style={{ 'font-size': '9px', ...mono, background: 'var(--danger-bg)', color: 'var(--danger)', 'border-radius': '4px', padding: '1px 6px' }}>ADMIN</span></Show>
          <Show when={!active()}><span style={{ 'font-size': '9px', ...mono, background: 'var(--danger-bg)', color: 'var(--danger)', 'border-radius': '4px', padding: '1px 6px' }}>DISABLED</span></Show>
          <Show when={props.user.must_change_password}><span style={{ 'font-size': '9px', ...mono, background: 'rgba(233,196,74,0.15)', color: '#c9a227', 'border-radius': '4px', padding: '1px 6px' }}>PWD RESET</span></Show>
        </div>
        <div style={{ 'font-size': '11px', color: 'var(--muted)', ...mono, 'margin-top': '1px' }}>{props.user.email}</div>
      </div>
      <div style={{ 'font-size': '11px', color: 'var(--muted)', ...mono, 'text-align': 'right', 'flex-shrink': '0' }}>
        <div>{translate('admin_user_designs').replace('{count}', String(props.user.design_count ?? 0))}</div>
        <div>{fmtBytes(parseInt(props.user.used_bytes || '0'))}</div>
      </div>
      <Show when={!isLastActiveAdmin()}>
        <button onClick={toggle} disabled={loading()} style={{ padding: '5px 12px', background: active() ? 'var(--danger-bg)' : 'var(--success-bg)', border: `1px solid ${active() ? 'var(--danger-border)' : 'var(--success-border)'}`, 'border-radius': '7px', color: active() ? 'var(--danger)' : 'var(--success)', 'font-size': '11px', cursor: 'pointer', ...mono, 'flex-shrink': '0' }}>
          {loading() ? '…' : active() ? translate('admin_disable') : translate('admin_enable')}
        </button>
      </Show>
      <button onClick={props.onDetail} style={{ padding: '5px 12px', background: 'var(--surface)', border: '1px solid var(--border2)', 'border-radius': '7px', color: 'var(--text3)', 'font-size': '11px', cursor: 'pointer', ...mono, 'flex-shrink': '0' }}>{translate('admin_user_details_btn')}</button>
    </div>
  )
}

function CreateUser(props: { onBack: () => void; showToast: (m: string, v?: string) => void }) {
  const { translate } = useI18n()
  const [form, setForm] = createSignal({ name: '', email: '', password: '', admin: false, must_change_password: false })
  const [saving, setSaving] = createSignal(false)
  const [err, setErr] = createSignal('')
  /** Merges a single field update into the create-user form state. */
  const updateField = (fieldKey: string, fieldValue: any) => setForm((currentState: any) => ({ ...currentState, [fieldKey]: fieldValue }))

  const save = async () => {
    if (!form().name.trim()) { setErr(translate('validation_username_required')); return }
    if (!form().password.trim()) { setErr(translate('validation_password_required')); return }
    setErr(''); setSaving(true)
    try {
      await api.adminCreateUser({ name: form().name, email: form().email, password: form().password, admin: form().admin ? 1 : 0, must_change_password: form().must_change_password ? 1 : 0 })
      props.showToast(translate('toast_user_created')); resetDirty(); props.onBack()
    } catch (failure: unknown) { setErr(translate(errorKey(failure))) }
    finally { setSaving(false) }
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '16px' }}>
      <div style={{ display: 'flex', 'align-items': 'center', gap: '10px' }}>
        <button onClick={props.onBack} style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', padding: '0', display: 'flex', 'align-items': 'center' }}>
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 18 9 12 15 6"/></svg>
        </button>
        <SectionTitle label={translate('admin_create_user')} />
      </div>
      <Card>
        <Err message={err()} />
        <div><label style={lbl}>{translate('field_username') + ' *'}</label><input style={inp} value={form().name} onInput={e => updateField('name', e.currentTarget.value)} /></div>
        <div><label style={lbl}>{translate('field_email')}</label><input style={inp} type="email" value={form().email} onInput={e => updateField('email', e.currentTarget.value)} /></div>
        <div><label style={lbl}>{translate('section_password') + ' *'}</label><input style={inp} type="password" value={form().password} onInput={e => updateField('password', e.currentTarget.value)} placeholder={translate('validation.password_short')} /></div>
        <Toggle value={form().admin} onChange={newValue => updateField('admin', newValue)} label={translate('admin_account_flag')} />
        <Toggle value={form().must_change_password} onChange={newValue => updateField('must_change_password', newValue)} label={translate('admin_must_change_pwd')} />
        <Btn onClick={save} loading={saving()} label={translate('admin_create_user')} />
      </Card>
    </div>
  )
}

// shortID renders a 32-character public id readably: the ends identify an
// account, the middle carries no information a human uses.
function shortID(value: string): string {
  return value && value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value
}

function UserDetail(props: { userId: string; adminCount: number; onBack: () => void; showToast: (m: string, v?: string) => void }) {
  const { translate, lang } = useI18n()
  const [data, setData] = createSignal<any>(null)
  const [loading, setLoading] = createSignal(true)
  const [editing, setEditing] = createSignal(false)
  const [editForm, setEditForm] = createSignal<any>({})
  const [saving, setSaving] = createSignal(false)
  const [err, setErr] = createSignal('')
  const [ok, setOk] = createSignal('')
  const [resetPwd, setResetPwd] = createSignal('')
  const [resetting, setResetting] = createSignal(false)
  const [confirmDelete, setConfirmDelete] = createSignal(false)
  const [deleting, setDeleting] = createSignal(false)
  const [toggling, setToggling] = createSignal(false)

  const load = () => {
    api.adminUserDetail(props.userId).then((r: any) => {
      setData(r.data)
      setEditForm({ name: r.data.user?.name || r.data.name, email: r.data.user?.email || r.data.email, admin: !!(r.data.user?.admin ?? r.data.admin), must_change_password: !!(r.data.user?.must_change_password ?? r.data.must_change_password) })
    }).catch(() => {}).finally(() => setLoading(false))
  }
  load()

  const userData = () => data()?.user || data() || {}

  const saveEdit = async () => {
    setErr(''); setSaving(true)
    try { await api.adminUpdateUser(props.userId, { ...editForm(), admin: editForm().admin ? 1 : 0, must_change_password: editForm().must_change_password ? 1 : 0 }); props.showToast(translate('toast_user_updated')); resetDirty(); setEditing(false); load() }
    catch (failure: unknown) { setErr(translate(errorKey(failure))) }
    finally { setSaving(false) }
  }

  const doReset = async () => {
    setErr(''); setOk('')
    if (!resetPwd()) { setErr(translate('validation_password_required')); return }
    setResetting(true)
    try {
      const res = await api.adminResetPassword(props.userId, { new_password: resetPwd() }) as { data: any }
      const pw = res.data?.new_password
      setOk(pw ? translate('admin_pwd_reset_with_new').replace('{password}', pw) : translate('admin_pwd_reset_success'))
      setResetPwd(''); load()
    } catch (failure: unknown) { setErr(translate(errorKey(failure))) }
    finally { setResetting(false) }
  }

  const deleteUser = async () => {
    setDeleting(true)
    try { await api.adminDeleteUser(props.userId); props.showToast(translate('toast_user_deleted')); props.onBack() }
    catch (failure: unknown) { props.showToast(translate('admin_delete_failed') + ': ' + translate(errorKey(failure))); setConfirmDelete(false) }
    finally { setDeleting(false) }
  }

  const toggleActive = async () => {
    setToggling(true); setErr(''); setOk('')
    const newState = userData().state === 'active' ? 'inactive' : 'active'
    try { await api.adminUpdateUser(props.userId, { state: newState }); props.showToast(newState === 'active' ? translate('toast_user_activated') : translate('toast_user_deactivated')); load() }
    catch (failure: unknown) { setErr(translate(errorKey(failure))) }
    finally { setToggling(false) }
  }

  const isLastAdmin = () => userData().admin && props.adminCount <= 1

  return (
    <Show when={!loading()} fallback={<Spinner />}>
      <Show when={data()}>
        <div style={{ display: 'flex', 'flex-direction': 'column', gap: '14px' }}>
          <div style={{ display: 'flex', 'align-items': 'center', gap: '10px' }}>
            <button onClick={props.onBack} style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', padding: '0', display: 'flex', 'align-items': 'center' }}>
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 18 9 12 15 6"/></svg>
            </button>
            <LetterAvatar name={userData().name} size={36} />
            <div>
              <div style={{ 'font-size': '14px', 'font-weight': '700', color: 'var(--text)', ...sans }}>{userData().name}</div>
              <div style={{ 'font-size': '11px', color: 'var(--muted)', ...mono }}>{userData().email}</div>
            </div>
            <div style={{ 'margin-left': 'auto', display: 'flex', gap: '8px' }}>
              <Show when={confirmDelete()} fallback={
                <>
                  {/* Deactivate / Activate - hidden only when last active admin tries to deactivate */}
                  <Show when={!(isLastAdmin() && userData().state === 'active')}>
                    <button onClick={toggleActive} disabled={toggling()}
                      style={{ padding: '5px 12px', background: userData().state === 'active' ? 'rgba(233,196,74,0.1)' : 'var(--success-bg)', border: `1px solid ${userData().state === 'active' ? 'rgba(201,162,39,0.4)' : 'var(--success-border)'}`, 'border-radius': '7px', color: userData().state === 'active' ? '#c9a227' : 'var(--success)', 'font-size': '11px', cursor: 'pointer', ...mono, 'flex-shrink': '0' }}>
                      {toggling() ? '…' : userData().state === 'active' ? translate('admin_deactivate') : translate('admin_activate')}
                    </button>
                  </Show>
                  {/* Delete - hidden when last admin */}
                  <Show when={!isLastAdmin()}>
                    <button onClick={() => setConfirmDelete(true)}
                      style={{ padding: '5px 12px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '7px', color: 'var(--danger)', 'font-size': '11px', cursor: 'pointer', ...mono, 'flex-shrink': '0' }}>
                      {translate('btn_delete_design')}
                    </button>
                  </Show>
                </>
              }>
                {/* Delete confirmation */}
                <div style={{ display: 'flex', gap: '8px', 'align-items': 'center' }}>
                  <span style={{ 'font-size': '11px', color: 'var(--danger)', ...mono }}>{translate('admin_delete_confirm')}</span>
                  <button onClick={deleteUser} disabled={deleting()} style={{ padding: '5px 12px', background: 'var(--danger-bg)', border: '1px solid var(--danger)', 'border-radius': '7px', color: 'var(--danger)', 'font-size': '11px', 'font-weight': '700', cursor: 'pointer', ...mono }}>{deleting() ? '…' : translate('btn_confirm_delete')}</button>
                  <button onClick={() => setConfirmDelete(false)} style={{ padding: '5px 10px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '7px', color: 'var(--text3)', 'font-size': '11px', cursor: 'pointer', ...mono }}>{translate('btn_cancel')}</button>
                </div>
              </Show>
            </div>
          </div>
          <Err message={err()} /><Ok message={ok()} />
          <Card>
            <Show when={!editing()} fallback={
              <div style={{ display: 'flex', 'flex-direction': 'column', gap: '10px' }}>
                <div><label style={lbl}>{translate('field_username')}</label><input style={inp} value={editForm().name} onInput={event => setEditForm((currentState: any) => ({ ...currentState, name: event.currentTarget.value }))} /></div>
                <div><label style={lbl}>{translate('field_email')}</label><input style={inp} type="email" value={editForm().email} onInput={event => setEditForm((currentState: any) => ({ ...currentState, email: event.currentTarget.value }))} /></div>
                <Toggle value={editForm().admin} onChange={newValue => setEditForm((currentState: any) => ({ ...currentState, admin: newValue }))} label={translate('admin_field_admin')} />
                <Toggle value={editForm().must_change_password} onChange={newValue => setEditForm((currentState: any) => ({ ...currentState, must_change_password: newValue }))} label={translate('admin_field_force_pwd')} />
                <div style={{ display: 'flex', gap: '8px' }}>
                  <Btn small label={translate('btn_save')} onClick={saveEdit} loading={saving()} />
                  <Btn small secondary label={translate('btn_cancel')} onClick={() => setEditing(false)} />
                </div>
              </div>
            }>
              <div style={{ display: 'grid', 'grid-template-columns': '1fr 1fr', gap: '12px' }}>
                <FieldView label={translate('admin_field_id')} value={shortID(userData().id)} mono />
                <FieldView label={translate('admin_field_state')} value={translate(`user_state_${userData().state}`)} />
                <FieldView label={translate('field_username')} value={userData().name} />
                <FieldView label={translate('field_email')} value={userData().email} />
                <FieldView label={translate('admin_field_admin')} value={userData().admin ? translate('label_yes') : translate('label_no')} />
                <FieldView label={translate('admin_field_force_pwd')} value={userData().must_change_password ? translate('label_yes') : translate('label_no')} />
                <FieldView label={translate('admin_field_created')} value={formatDate(userData().created_at, lang())} />
                <FieldView label={translate('admin_field_updated')} value={formatDate(userData().updated_at, lang())} />
              </div>
              <Btn small label={translate('admin_edit_profile')} onClick={() => setEditing(true)} />
            </Show>
            <div style={{ 'border-top': '1px solid var(--border)', 'padding-top': '14px', display: 'flex', 'flex-direction': 'column', gap: '10px' }}>
              <div style={{ 'font-size': '11px', color: 'var(--muted)', ...mono, 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }}>{translate('admin_reset_password')}</div>
              <div style={{ display: 'flex', gap: '8px' }}>
                <input style={{ ...inp, flex: '1' }} type="password" placeholder={translate('admin_new_pwd_placeholder')} value={resetPwd()} onInput={e => setResetPwd(e.currentTarget.value)} />
                <Btn small label={translate('admin_reset_password')} onClick={doReset} loading={resetting()} danger />
              </div>
            </div>
          </Card>
        </div>
      </Show>
    </Show>
  )
}

/**
 * Admin user management tab - lists all users with activate/deactivate buttons
 * and drills into a per-user detail/edit view or the create-user form.
 */
function TabUsers(props: { showToast: (m: string, v?: string) => void }) {
  const { translate } = useI18n()
  const [users, setUsers] = createSignal<any[]>([])
  const [loading, setLoading] = createSignal(true)
  const [err, setErr] = createSignal('')
  const [detail, setDetail] = createSignal<string | null>(null)
  const [creating, setCreating] = createSignal(false)

  const load = () => {
    setLoading(true); setErr('')
    api.adminListUsers().then((r: any) => setUsers(r.data || [])).catch((failure: unknown) => setErr(translate(errorKey(failure)))).finally(() => setLoading(false))
  }
  load()

  const adminCount = () => users().filter((u: any) => u.admin && u.state === 'active').length

  return (
    <Show when={!detail() && !creating()} fallback={
      <Show when={detail()} fallback={<CreateUser onBack={() => { setCreating(false); load() }} showToast={props.showToast} />}>
        <UserDetail userId={detail()!} adminCount={adminCount()} onBack={() => { setDetail(null); load() }} showToast={props.showToast} />
      </Show>
    }>
      <div style={{ display: 'flex', 'flex-direction': 'column', gap: '16px' }}>
        <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between' }}>
          <SectionTitle label={translate('admin_user_management')} />
          <Btn small label={translate('admin_add_user')} onClick={() => setCreating(true)} />
        </div>
        <Show when={loading()} fallback={
          <Show when={err()} fallback={
            <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px' }}>
              <For each={users()}>{u =>
                <UserRow user={u} adminCount={adminCount()} onDetail={() => setDetail(u.id)} onToggle={load} showToast={props.showToast} />
              }</For>
              <Show when={users().length === 0}>
                <div style={{ color: 'var(--muted)', ...mono, 'font-size': '13px', 'text-align': 'center', padding: '20px' }}>{translate('admin_no_users')}</div>
              </Show>
            </div>
          }>
            <Err message={err()} />
          </Show>
        }>
          <Spinner />
        </Show>
      </div>
    </Show>
  )
}

/**
 * Admin info tab - shows the app version/description and a live system-health
 * check list with status indicators for each service component.
 */
function TabInfo() {
  const { translate } = useI18n()
  const [health, setHealth] = createSignal<any>(null)
  const [loading, setLoading] = createSignal(true)

  const load = () => { setLoading(true); api.adminHealth().then((r: any) => setHealth(r.data)).catch(() => setHealth(null)).finally(() => setLoading(false)) }
  load()

  // 'off' is a subsystem switched off by a setting rather than a fault, but it
  // gets the danger colours all the same: it means nothing is being synced, and
  // in green with a footnote it read as "all good".
  const statusColor = (status: string) => status === 'ok' ? 'var(--success)' : status === 'warn' ? '#e9c46a' : 'var(--danger)'
  const statusBackground = (status: string) => status === 'ok' ? 'var(--success-bg)' : status === 'warn' ? 'rgba(233,196,74,0.1)' : 'var(--danger-bg)'

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '20px' }}>
      <Card>
        <SectionTitle label={translate('admin_about_title')} />
        <div style={{ display: 'flex', 'align-items': 'center', gap: '16px' }}>
          <div style={{ width: '52px', height: '52px', 'border-radius': '14px', background: 'var(--accent)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '28px', 'flex-shrink': '0' }}>🖨️</div>
          <div>
            <div style={{ 'font-size': '20px', 'font-weight': '800', color: 'var(--text)', ...sans, 'letter-spacing': '-0.02em' }}>Mesh<span style={{ color: 'var(--accent)' }}>Depot</span></div>
            <div style={{ 'font-size': '12px', color: 'var(--muted)', ...mono }}>Version 1.1.0</div>
          </div>
        </div>
        <div style={{ 'font-size': '12px', color: 'var(--muted)', ...mono, 'line-height': '1.8', 'border-top': '1px solid var(--border)', 'padding-top': '12px' }}>
          {translate('admin_about_desc')}
        </div>
      </Card>
      <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between' }}>
        <SectionTitle label={translate('admin_system_health')} />
        <button onClick={load} style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', 'font-size': '12px', ...mono }}>{translate('admin_refresh')}</button>
      </div>
      <Show when={loading()} fallback={
        <Show when={health()} fallback={<Err message={translate('admin_health_failed')} />}>
          <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px' }}>
            <For each={Object.entries(health().checks || health())}>{([key, check]: [string, any]) =>
              <div style={{ display: 'flex', 'align-items': 'center', gap: '12px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '10px', padding: '12px 16px' }}>
                <div style={{ width: '10px', height: '10px', 'border-radius': '50%', background: statusColor(check.status ?? 'ok'), 'flex-shrink': '0' }} />
                <div style={{ flex: '1' }}>
                  <div style={{ 'font-size': '13px', 'font-weight': '600', color: 'var(--text)', ...sans }}>{check.label_key ? translate(check.label_key, check.label_vars) : (check.label ?? key)}</div>
                  <Show when={check.message_key || check.message}>
                    <div style={{ 'font-size': '11px', color: 'var(--muted)', ...mono, 'margin-top': '1px' }}>{check.message_key ? translate(check.message_key, check.vars) : check.message}</div>
                    <Show when={check.detail}><div style={{ 'font-size': '10px', color: 'var(--muted)', ...mono, 'margin-top': '2px', opacity: '0.6' }}>{check.detail}</div></Show>
                  </Show>
                </div>
                <span style={{ 'font-size': '10px', padding: '2px 8px', 'border-radius': '5px', background: statusBackground(check.status ?? 'ok'), color: statusColor(check.status ?? 'ok'), ...mono, 'font-weight': '700', 'text-transform': 'uppercase' }}>{translate('health_status_' + (check.status ?? 'ok'))}</span>
              </div>
            }</For>
          </div>
        </Show>
      }>
        <Spinner />
      </Show>
    </div>
  )
}

/**
 * Admin settings tab - configures server-wide settings such as the library
 * sync hour.
 */
const COOLDOWN_PLATFORMS = ['printables', 'thingiverse', 'makerworld', 'thangs', 'cults3d', 'myminifactory'] as const
type CooldownPlatform = typeof COOLDOWN_PLATFORMS[number]

/**
 * Lower bound per platform, mirroring settingsSchema in the backend. Below 30 s
 * the platforms start rate-limiting; makerworld answers a burst with a captcha
 * that nothing here can solve, so it needs a much wider gap.
 */
/** Shortest design update interval the backend accepts (scheduler.DesignUpdateMinDays). */
const DESIGN_UPDATE_MIN_DAYS = 7

const COOLDOWN_MIN: Record<CooldownPlatform, number> = {
  printables: 30, thingiverse: 30, makerworld: 200, thangs: 30, cults3d: 30, myminifactory: 30,
}

// Defaults for the auto-pause settings, mirroring the backend seeds.
const BLOCK_THRESHOLD_DEFAULT = 3
const BLOCK_HOURS_DEFAULT = 24

function TabSettings(props: { showToast: (m: string, v?: string) => void }) {
  const { translate, lang } = useI18n()
  const [syncHour, setSyncHour] = createSignal(3)
  const [syncEnabled, setSyncEnabled] = createSignal(true)
  const [saving, setSaving] = createSignal(false)
  const [err, setErr] = createSignal('')

  // Design updates: re-download of designs already in the library. A separate
  // card because it hits the platforms independently of the library sync.
  const [updateEnabled, setUpdateEnabled] = createSignal(true)
  const [updateDays, setUpdateDays] = createSignal(DESIGN_UPDATE_MIN_DAYS)
  const [updateSaving, setUpdateSaving] = createSignal(false)
  const [updateErr, setUpdateErr] = createSignal('')

  const [cooldowns, setCooldowns] = createSignal<Record<CooldownPlatform, number>>({
    printables: 30, thingiverse: 30, makerworld: 300, thangs: 30, cults3d: 30, myminifactory: 30,
  })
  const [cooldownSaving, setCooldownSaving] = createSignal(false)
  const [cooldownErr, setCooldownErr] = createSignal('')

  // Auto-pause: after N anti-bot/rate-limit hits in a row a platform's queue is
  // paused for a number of hours and resumes on its own. queueBlocks holds the
  // current per-platform state so the admin can see and lift an active pause.
  const [blockThreshold, setBlockThreshold] = createSignal(BLOCK_THRESHOLD_DEFAULT)
  const [blockHours, setBlockHours] = createSignal(BLOCK_HOURS_DEFAULT)
  const [blockSaving, setBlockSaving] = createSignal(false)
  const [blockErr, setBlockErr] = createSignal('')
  const [queueBlocks, setQueueBlocks] = createSignal<QueueBlock[]>([])

  const applySettings = (d: any) => {
    setSyncHour(d.library_sync_hour ?? 3)
    setSyncEnabled((d.library_sync_enabled ?? 1) === 1)
    setUpdateEnabled((d.design_update_enabled ?? 1) === 1)
    setUpdateDays(d.design_update_min_days ?? DESIGN_UPDATE_MIN_DAYS)
    setCooldowns({
      printables:    d.download_cooldown_printables    ?? 30,
      thingiverse:   d.download_cooldown_thingiverse   ?? 30,
      makerworld:    d.download_cooldown_makerworld    ?? 300,
      thangs:        d.download_cooldown_thangs        ?? 30,
      cults3d:       d.download_cooldown_cults3d       ?? 30,
      myminifactory: d.download_cooldown_myminifactory ?? 30,
    })
    setBlockThreshold(d.queue_block_threshold ?? BLOCK_THRESHOLD_DEFAULT)
    setBlockHours(d.queue_block_hours ?? BLOCK_HOURS_DEFAULT)
    setQueueBlocks(Array.isArray(d.queue_blocks) ? d.queue_blocks : [])
  }
  const reloadSettings = () => api.adminGetSettings().then((r: any) => applySettings(r.data ?? {})).catch(() => {})
  reloadSettings()

  const save = async () => {
    setSaving(true); setErr('')
    try {
      await api.adminSaveSettings({ library_sync_hour: syncHour(), library_sync_enabled: syncEnabled() ? 1 : 0 })
      resetDirty()
      props.showToast(translate('toast_settings_saved'))
    } catch (failure: unknown) { setErr(translate(errorKey(failure))) }
    finally { setSaving(false) }
  }

  const saveDesignUpdates = async () => {
    if (updateDays() < DESIGN_UPDATE_MIN_DAYS) {
      setUpdateErr(translate('error.design_update_interval_too_low'))
      return
    }
    setUpdateSaving(true); setUpdateErr('')
    try {
      await api.adminSaveSettings({ design_update_enabled: updateEnabled() ? 1 : 0, design_update_min_days: updateDays() })
      resetDirty()
      props.showToast(translate('toast_settings_saved'))
    } catch (failure: unknown) { setUpdateErr(translate(errorKey(failure))) }
    finally { setUpdateSaving(false) }
  }

  // The same limits are enforced in the backend; checking here as well names the
  // offending platform instead of leaving the admin to guess which field the
  // server complained about.
  const cooldownViolation = (): string => {
    for (const platform of COOLDOWN_PLATFORMS) {
      if (cooldowns()[platform] >= COOLDOWN_MIN[platform]) continue
      return translate(platform === 'makerworld' ? 'error.cooldown_makerworld_too_low' : 'error.cooldown_too_low')
    }
    return ''
  }

  const saveCooldowns = async () => {
    const violation = cooldownViolation()
    if (violation) { setCooldownErr(violation); return }
    setCooldownSaving(true); setCooldownErr('')
    try {
      const payload: Record<string, number> = {}
      for (const p of COOLDOWN_PLATFORMS) payload[`download_cooldown_${p}`] = cooldowns()[p]
      await api.adminSaveSettings(payload)
      resetDirty()
      props.showToast(translate('toast_settings_saved'))
    } catch (failure: unknown) { setCooldownErr(translate(errorKey(failure))) }
    finally { setCooldownSaving(false) }
  }

  const saveBlockSettings = async () => {
    setBlockSaving(true); setBlockErr('')
    try {
      await api.adminSaveSettings({ queue_block_threshold: blockThreshold(), queue_block_hours: blockHours() })
      resetDirty()
      props.showToast(translate('toast_settings_saved'))
    } catch (failure: unknown) { setBlockErr(translate(errorKey(failure))) }
    finally { setBlockSaving(false) }
  }

  const pausePlatform = async (platform: string) => {
    try { await api.adminPauseQueue(platform); await reloadSettings() }
    catch (failure: unknown) { setBlockErr(translate(errorKey(failure))) }
  }
  const resumePlatform = async (platform: string) => {
    try { await api.adminResumeQueue(platform); await reloadSettings() }
    catch (failure: unknown) { setBlockErr(translate(errorKey(failure))) }
  }
  const blockOf = (platform: string): QueueBlock | undefined => queueBlocks().find(block => block.platform === platform)
  const formatWhen = (iso?: string) => iso ? formatDateTime(iso, lang()) : ''

  const numInp: JSX.CSSProperties = { ...inp, width: '72px', 'text-align': 'right' }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '20px' }}>
      <CollapsibleCard label={translate('admin_library_sync_heading')}
        status={syncEnabled() ? translate('label_active') : translate('label_inactive')} statusOk={syncEnabled()} statusOff={!syncEnabled()}>
        <div style={{ ...sans, 'font-size': '12px', color: 'var(--text3)', 'line-height': '1.6' }}>
          {translate('admin_library_sync_desc')}
        </div>
        <Err message={err()} />
        <div>
          <Toggle value={syncEnabled()} onChange={setSyncEnabled} label={translate('admin_library_sync_enabled_label')} />
          <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '6px' }}>
            {translate('admin_library_sync_enabled_hint')}
          </div>
        </div>
        <div style={{ display: 'flex', 'align-items': 'flex-end', gap: '12px', 'flex-wrap': 'wrap' }}>
          {/* No point in picking an hour for a sync that never runs. */}
          <Show when={syncEnabled()}>
            <div style={{ flex: '1', 'min-width': '160px' }}>
              <label style={lbl}>{translate('admin_library_sync_time_label')}</label>
              <select
                style={{ ...inp, cursor: 'pointer' }}
                value={syncHour()}
                onChange={e => setSyncHour(parseInt(e.currentTarget.value))}>
                <For each={Array.from({ length: 24 }, (_, i) => i)}>{i =>
                  <option value={i}>{String(i).padStart(2, '0')}:00</option>
                }</For>
              </select>
            </div>
          </Show>
          {/* No "run now" here. This card configures the scheduled sync for the
              whole server; the manual run belongs to the account that owns the
              platform credentials and lives in its sync settings. */}
          <div style={{ display: 'flex', gap: '8px' }}>
            <Btn
              label={translate('admin_library_sync_save')}
              onClick={save}
              loading={saving()}
            />
          </div>
        </div>
      </CollapsibleCard>

      <CollapsibleCard label={translate('admin_design_update_heading')}
        status={updateEnabled() ? translate('label_active') : translate('label_inactive')} statusOk={updateEnabled()} statusOff={!updateEnabled()}>
        <div style={{ ...sans, 'font-size': '12px', color: 'var(--text3)', 'line-height': '1.6' }}>
          {translate('admin_design_update_desc')}
        </div>
        <Err message={updateErr()} />
        <div>
          <Toggle value={updateEnabled()} onChange={setUpdateEnabled} label={translate('admin_design_update_enabled_label')} />
          <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '6px' }}>
            {translate('admin_design_update_enabled_hint')}
          </div>
        </div>
        {/* An interval for updates that never run would only be misleading. */}
        <Show when={updateEnabled()}>
          <div>
            <label style={lbl}>{translate('admin_design_update_days_label')}</label>
            <div style={{ display: 'flex', 'align-items': 'center', gap: '8px' }}>
              <input
                type="number"
                min={DESIGN_UPDATE_MIN_DAYS}
                value={updateDays()}
                onInput={event => setUpdateDays(parseInt(event.currentTarget.value) || 0)}
                style={{ ...numInp, border: updateDays() < DESIGN_UPDATE_MIN_DAYS ? '1px solid var(--danger-border)' : (inp.border as string) }}
              />
              <span style={{ ...sans, 'font-size': '13px', color: 'var(--text3)' }}>{translate('unit_days')}</span>
            </div>
            <div style={{ ...sans, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '6px' }}>
              {translate('admin_design_update_days_hint', { min: DESIGN_UPDATE_MIN_DAYS })}
            </div>
          </div>
        </Show>
        <div>
          <Btn label={translate('admin_library_sync_save')} onClick={saveDesignUpdates} loading={updateSaving()} />
        </div>
      </CollapsibleCard>

      <CollapsibleCard label={translate('admin_download_cooldown_heading')}>
        <div style={{ ...sans, 'font-size': '12px', color: 'var(--text3)', 'line-height': '1.6' }}>
          {translate('admin_download_cooldown_desc')}
        </div>
        <Err message={cooldownErr()} />
        <div style={{ display: 'grid', 'grid-template-columns': '1fr auto auto', gap: '8px 12px', 'align-items': 'center', 'margin-top': '8px' }}>
          <For each={COOLDOWN_PLATFORMS}>{platform =>
            <>
              <label style={{ ...sans, 'font-size': '13px', 'text-transform': 'capitalize' }}>
                {platform}
                <span style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'margin-left': '8px' }}>
                  {translate('admin_cooldown_min', { min: COOLDOWN_MIN[platform] })}
                </span>
              </label>
              <input
                type="number" min={COOLDOWN_MIN[platform]} max="3600"
                style={{ ...numInp, ...(cooldowns()[platform] < COOLDOWN_MIN[platform] ? { 'border-color': 'var(--danger)' } : {}) }}
                value={cooldowns()[platform]}
                onInput={e => setCooldowns(prev => ({ ...prev, [platform]: Math.max(0, parseInt(e.currentTarget.value) || 0) }))}
              />
              <span style={{ ...mono, 'font-size': '12px', color: 'var(--muted)' }}>{translate('unit_seconds')}</span>
            </>
          }</For>
        </div>
        <div style={{ 'margin-top': '12px' }}>
          <Btn
            label={translate('admin_library_sync_save')}
            onClick={saveCooldowns}
            loading={cooldownSaving()}
          />
        </div>
      </CollapsibleCard>

      <CollapsibleCard label={translate('admin_queue_block_heading')}>
        <div style={{ ...sans, 'font-size': '12px', color: 'var(--text3)', 'line-height': '1.6' }}>
          {translate('admin_queue_block_desc')}
        </div>
        <Err message={blockErr()} />
        <div style={{ display: 'flex', gap: '20px', 'flex-wrap': 'wrap', 'margin-top': '8px' }}>
          <div>
            <label style={lbl}>{translate('admin_queue_block_threshold_label')}</label>
            <input type="number" min="1" max="20" style={numInp}
              value={blockThreshold()}
              onInput={e => setBlockThreshold(Math.max(1, parseInt(e.currentTarget.value) || 1))} />
          </div>
          <div>
            <label style={lbl}>{translate('admin_queue_block_hours_label')}</label>
            <input type="number" min="1" max="720" style={numInp}
              value={blockHours()}
              onInput={e => setBlockHours(Math.max(1, parseInt(e.currentTarget.value) || 1))} />
          </div>
        </div>
        <div style={{ 'margin-top': '12px' }}>
          <Btn label={translate('admin_library_sync_save')} onClick={saveBlockSettings} loading={blockSaving()} />
        </div>

        <div style={{ 'border-top': '1px solid var(--border)', 'margin-top': '16px', 'padding-top': '14px' }}>
          <div style={{ ...sans, 'font-size': '13px', 'font-weight': '600', color: 'var(--text)', 'margin-bottom': '10px' }}>
            {translate('admin_queue_status_heading')}
          </div>
          <div style={{ display: 'flex', 'flex-direction': 'column', gap: '8px' }}>
            <For each={COOLDOWN_PLATFORMS}>{platform => {
              const block = () => blockOf(platform)
              const suspended = () => !!block()?.paused || !!block()?.blocked
              return (
                <div style={{ display: 'flex', 'align-items': 'center', gap: '12px' }}>
                  <span style={{ ...sans, 'font-size': '13px', 'text-transform': 'capitalize', width: '110px', 'flex-shrink': '0', color: PLATFORM_COLORS[platform] ?? 'var(--text)' }}>{platform}</span>
                  <span style={{ ...mono, 'font-size': '11px', flex: '1', color: suspended() ? 'var(--danger)' : 'var(--muted)' }}>
                    <Show when={block()?.blocked} fallback={block()?.paused ? translate('admin_queue_paused') : translate('admin_queue_active')}>
                      {translate('admin_queue_blocked_until', { until: formatWhen(block()?.until) })}
                    </Show>
                  </span>
                  <Show when={suspended()} fallback={<Btn small label={translate('admin_queue_pause')} onClick={() => pausePlatform(platform)} />}>
                    <Btn small label={translate('admin_queue_resume')} onClick={() => resumePlatform(platform)} />
                  </Show>
                </div>
              )
            }}</For>
          </div>
        </div>
      </CollapsibleCard>
    </div>
  )
}

/**
 * Admin translation tab - toggles automatic translation (Google Translate,
 * free/no key) and offers a backfill run that translates all not-yet-translated
 * designs in batches.
 */
function TabTranslation(props: { showToast: (m: string, v?: string) => void }) {
  const { translate } = useI18n()
  const [enabled, setEnabled] = createSignal(true)
  const [saving, setSaving] = createSignal(false)
  const [err, setErr] = createSignal('')
  const [backfillRunning, setBackfillRunning] = createSignal(false)
  const [backfillStatus, setBackfillStatus] = createSignal('')

  api.adminGetSettings().then((r: any) => setEnabled((r.data?.translation_enabled ?? 1) === 1)).catch(() => {})

  const save = async () => {
    setSaving(true); setErr('')
    try {
      await api.adminSaveSettings({ translation_enabled: enabled() ? 1 : 0 })
      resetDirty()
      props.showToast(translate('toast_settings_saved'))
    } catch (failure: unknown) { setErr(translate(errorKey(failure))) }
    finally { setSaving(false) }
  }

  /** Runs backfill batches until everything is translated or no progress is made. */
  const runBackfill = async () => {
    setBackfillRunning(true); setErr(''); setBackfillStatus('')
    let translated = 0
    let failed = 0
    try {
      while (true) {
        const r: any = await api.adminTranslationBackfill()
        const d = r.data ?? {}
        if (!d.configured) { setErr(translate('admin_translation_disabled')); break }
        translated += d.processed ?? 0
        failed += d.failed ?? 0
        setBackfillStatus(translate('admin_translation_backfill_progress', { done: translated, remaining: d.remaining ?? 0 }))
        if ((d.remaining ?? 0) <= 0 || (d.processed ?? 0) === 0) {
          setBackfillStatus(translate('admin_translation_backfill_done', { done: translated, failed }))
          break
        }
      }
    } catch (failure: unknown) { setErr(translate(errorKey(failure))) }
    finally { setBackfillRunning(false) }
  }

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '20px' }}>
      <Card>
        <SectionTitle label={translate('admin_translation_heading')} />
        <div style={{ ...sans, 'font-size': '12px', color: 'var(--text3)', 'line-height': '1.6' }}>
          {translate('admin_translation_desc')}
        </div>
        <Err message={err()} />
        <div style={{ 'margin-top': '12px' }}>
          <Toggle value={enabled()} onChange={setEnabled} label={translate('admin_translation_enable_label')} />
        </div>
        <div style={{ ...sans, 'font-size': '11px', color: 'var(--text3)', 'margin-top': '6px' }}>
          {translate('admin_translation_provider_hint')}
        </div>
        <div style={{ 'margin-top': '12px' }}>
          <Btn label={translate('admin_library_sync_save')} onClick={save} loading={saving()} />
        </div>
      </Card>

      <Card>
        <SectionTitle label={translate('admin_translation_backfill_heading')} />
        <div style={{ ...sans, 'font-size': '12px', color: 'var(--text3)', 'line-height': '1.6' }}>
          {translate('admin_translation_backfill_desc')}
        </div>
        <Show when={backfillStatus()}>
          <div style={{ ...mono, 'font-size': '12px', color: 'var(--accent-light)', 'margin-top': '8px' }}>{backfillStatus()}</div>
        </Show>
        <div style={{ 'margin-top': '12px' }}>
          <Btn
            label={backfillRunning() ? translate('admin_translation_backfill_running') : translate('admin_translation_backfill_btn')}
            onClick={runBackfill}
            loading={backfillRunning()}
          />
        </div>
      </Card>
    </div>
  )
}

/**
 * Full-screen admin settings modal with a sidebar tab navigation.
 * Tabs: Stats · Users · Info (system health).
 * Only reachable by users with the admin flag.
 */
export function ServerSettingsModal(props: { user: User; onClose: () => void; showToast: (m: string, v?: string) => void }) {
  const { translate } = useI18n()
  const [tab, setTab] = createSignal('stats')
  const { markDirty, guardClose } = useUnsavedChanges(translate('confirm_discard_changes'))
  // resetDirty is imported directly from the singleton
  createEffect(() => { document.body.style.overflow = 'hidden'; onCleanup(() => { document.body.style.overflow = '' }) })

  const TABS = [
    { key: 'stats',    label: translate('admin_tab_stats'),     icon: <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="6" y1="20" x2="6" y2="14"/></svg> },
    { key: 'users',    label: translate('admin_tab_users'),     icon: <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg> },
    { key: 'settings', label: translate('admin_tab_settings'),  icon: <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg> },
    { key: 'translation', label: translate('admin_tab_translation'), icon: <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg> },
    // Last on purpose: nothing here is set or changed, it is only looked up.
    { key: 'info',     label: translate('admin_tab_info'),      icon: <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg> },
  ]

  const sideButton = (active: boolean): JSX.CSSProperties => ({ width: '100%', height: '42px', padding: '0 16px', background: active ? 'rgba(69,123,157,0.12)' : 'none', border: 'none', 'border-left': active ? '2px solid var(--accent)' : '2px solid transparent', color: active ? 'var(--accent-light)' : 'var(--muted)', 'font-family': "'DM Sans', sans-serif", 'font-size': '13px', 'font-weight': active ? '600' : '400', cursor: 'pointer', display: 'flex', 'align-items': 'center', gap: '10px', 'text-align': 'left' })

  return (
    <div style={{ position: 'fixed', inset: '0', background: 'rgba(0,0,0,0.8)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '2000', 'backdrop-filter': 'blur(6px)' }}>
      <div class="stlv-modal-wide" style={{ background: 'var(--bg)', 'border-radius': '20px', width: '780px', height: 'min(96vh, 800px)', display: 'flex', 'flex-direction': 'column', overflow: 'hidden', 'box-shadow': '0 40px 100px rgba(0,0,0,0.5)', border: '1px solid var(--border)' }}>
        <div style={{ position: 'relative', height: '56px', 'border-bottom': '1px solid var(--border)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'flex-shrink': '0' }}>
          <span style={{ 'font-family': "'DM Sans', sans-serif", 'font-weight': '700', 'font-size': '15px', color: 'var(--accent)' }}>
            {translate('admin_settings_title')}
          </span>
          <button onClick={() => guardClose(props.onClose)} style={{ position: 'absolute', right: '18px', top: '0', height: '100%', display: 'flex', 'align-items': 'center', background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', 'font-size': '18px', 'line-height': '1' }}>×</button>
        </div>
        <div style={{ flex: '1', display: 'flex', overflow: 'hidden' }}>
          <div style={{ width: '190px', background: 'var(--bg2)', 'border-right': '1px solid var(--border)', display: 'flex', 'flex-direction': 'column', 'flex-shrink': '0' }}>
            <div>
            <For each={TABS}>{tabItem =>
              <button onClick={() => setTab(tabItem.key)} style={sideButton(tab() === tabItem.key)}>
                <span style={{ color: tab() === tabItem.key ? 'var(--accent-light)' : 'var(--muted)', 'flex-shrink': '0' }}>{tabItem.icon}</span>
                {tabItem.label}
              </button>
            }</For>
          </div>
        </div>
        <div style={{ flex: '1', 'overflow-y': 'auto', padding: '10px 28px 28px', background: 'var(--bg)' }} onInput={markDirty} onChange={markDirty}>
          <Show when={tab() === 'stats'}><TabStats /></Show>
          <Show when={tab() === 'users'}><TabUsers showToast={props.showToast} /></Show>
          <Show when={tab() === 'settings'}><TabSettings showToast={props.showToast} /></Show>
          <Show when={tab() === 'translation'}><TabTranslation showToast={props.showToast} /></Show>
          <Show when={tab() === 'info'}><TabInfo /></Show>
        </div>
        </div>
      </div>
    </div>
  )
}
