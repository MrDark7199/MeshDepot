import { UserAvatar } from './UserAvatar'
import { useI18n } from '../i18n/index'
import { Show } from 'solid-js'

export function UserDropdown(props: { user: any; onLogout: () => void; onAccountSettings: () => void; onServerSettings: () => void }) {
  const { translate } = useI18n()
  const buttonStyle: any = { width:'100%', padding:'12px 17px', background:'var(--surface)', border:'1px solid var(--border)', 'border-radius':'11px', color:'var(--text2)', 'font-family':"'DM Sans',sans-serif", 'font-size':'15px', 'font-weight':'500', cursor:'pointer', display:'flex', 'align-items':'center', 'justify-content':'center', gap:'10px' }
  return (
    <div style={{ position:'absolute', top:'calc(100% + 16px)', right:'0', 'z-index':'1000', background:'var(--bg)', 'border-radius':'20px', border:'1px solid var(--border)', 'box-shadow':'0 24px 70px rgba(0,0,0,0.4)', overflow:'hidden', width:'290px' }}>
      <div style={{ background:'var(--bg3)', padding:'24px 22px 20px', 'border-bottom':'1px solid var(--border)' }}>
        <div style={{ display:'flex', 'flex-direction':'column', 'align-items':'center', gap:'8px' }}>
          <UserAvatar name={props.user.name || props.user.email || '?'} avatarUrl={props.user.avatar_url} size={72} fontSize={26} />
          <div style={{ 'font-family':"'DM Sans',sans-serif", 'font-weight':'700', color:'var(--text)', 'font-size':'17px', 'margin-top':'4px' }}>{props.user.name}</div>
          <Show when={props.user.email}>
            <div style={{ 'font-family':"'DM Mono',monospace", 'font-size':'13px', color:'var(--muted)' }}>{props.user.email}</div>
          </Show>
        </div>
        <div style={{ 'margin-top':'18px', display:'flex', 'flex-direction':'column', gap:'8px' }}>
          <button onClick={props.onAccountSettings} style={buttonStyle}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
              <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>
            </svg>
            {translate('account_settings_title')}
          </button>
          <Show when={props.user.admin}>
            <button onClick={props.onServerSettings} style={buttonStyle}>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                <rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
              </svg>
              {translate('admin_settings_title')}
            </button>
          </Show>
        </div>
      </div>
      <div style={{ background:'var(--bg3)', padding:'13px 22px' }}>
        <button onMouseDown={e => { e.preventDefault(); e.stopPropagation(); props.onLogout() }}
          style={{ width:'100%', padding:'12px 17px', background:'var(--danger-bg)', border:'1px solid var(--danger-border)', 'border-radius':'11px', color:'var(--danger)', 'font-family':"'DM Sans',sans-serif", 'font-size':'15px', 'font-weight':'600', cursor:'pointer', display:'flex', 'align-items':'center', 'justify-content':'center', gap:'10px' }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
            <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
            <polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/>
          </svg>
          {translate('btn_sign_out')}
        </button>
      </div>
    </div>
  )
}
