import { createSignal, JSX } from 'solid-js'
import { useAuth } from '../services/AuthContext'
import { useI18n } from '../i18n/index'

export default function LoginPage() {
  const { login, totpVerify } = useAuth()
  const { translate }         = useI18n()
  const [identifier, setIdentifier] = createSignal('')
  const [password, setPassword]     = createSignal('')
  const [showPw, setShowPw]         = createSignal(false)
  const [remember, setRemember]     = createSignal(false)
  const [error, setError]           = createSignal('')
  const [loading, setLoading]       = createSignal(false)

  // TOTP step
  const [totpRequired, setTotpRequired]     = createSignal(false)
  const [pendingToken, setPendingToken]     = createSignal('')
  const [totpCode, setTotpCode]             = createSignal('')

  const handleSubmit = async () => {
    if (!identifier() || !password()) { setError(translate('error.credentials_required') || 'Please fill in all fields'); return }
    setError(''); setLoading(true)
    try {
      const res = await login(identifier(), password(), remember())
      if (res.totp_required && res.pending_token) {
        setPendingToken(res.pending_token)
        setTotpRequired(true)
      }
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : 'Login failed'
      setError(translate(message) !== message ? translate(message) : message)
    } finally { setLoading(false) }
  }

  const handleTotpSubmit = async () => {
    if (!totpCode()) return
    setError(''); setLoading(true)
    try {
      await totpVerify(pendingToken(), totpCode(), remember())
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : 'Invalid code'
      setError(translate(message) !== message ? translate(message) : message)
    } finally { setLoading(false) }
  }

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter') totpRequired() ? handleTotpSubmit() : handleSubmit()
  }

  const inputStyle: JSX.CSSProperties = { width: '100%', background: 'var(--input-bg)', border: '1px solid var(--border2)', 'border-radius': '10px', padding: '11px 14px', color: 'var(--text)', 'font-family': "'DM Sans', sans-serif", 'font-size': '14px', outline: 'none', 'box-sizing': 'border-box' }
  const labelStyle: JSX.CSSProperties = { display: 'block', 'font-size': '11px', 'font-family': "'DM Mono', monospace", color: 'var(--muted)', 'margin-bottom': '6px', 'text-transform': 'uppercase', 'letter-spacing': '0.05em' }

  return (
    <div style={{ 'min-height': '100vh', background: 'var(--bg)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-family': "'DM Sans', sans-serif" }}>
      <style>{`
        @keyframes spin { to { transform: rotate(360deg); } }
        input:-webkit-autofill { -webkit-box-shadow: 0 0 0 30px var(--input-bg) inset !important; -webkit-text-fill-color: var(--text) !important; }
        .login-btn:hover:not(:disabled) { filter: brightness(1.1); }
        .remember-cb { appearance: none; -webkit-appearance: none; width: 16px; height: 16px; border: 1px solid var(--border2); border-radius: 4px; background: var(--input-bg); cursor: pointer; flex-shrink: 0; position: relative; transition: background 0.15s, border 0.15s; }
        .remember-cb:checked { background: var(--accent); border-color: var(--accent); }
        .remember-cb:checked::after { content: ''; position: absolute; left: 4px; top: 1px; width: 5px; height: 9px; border: 2px solid #fff; border-top: none; border-left: none; transform: rotate(45deg); }
      `}</style>

      <div style={{ width: '400px', padding: '44px 38px', background: 'var(--bg2)', 'border-radius': '22px', 'box-shadow': '0 20px 60px rgba(0,0,0,0.35)', border: '1px solid var(--border)' }}>
        <div style={{ 'text-align': 'center', 'margin-bottom': '34px' }}>
          <div style={{ 'font-weight': '700', 'font-size': '30px', color: 'var(--text)', 'letter-spacing': '-0.02em' }}>
            Mesh<span style={{ color: 'var(--accent)' }}>Depot</span>
          </div>
          <div style={{ 'margin-top': '6px', 'font-size': '13px', color: 'var(--muted)', 'font-family': "'DM Mono', monospace" }}>
            {totpRequired() ? translate('login.totp_title') || 'Two-factor authentication' : '3D print design manager'}
          </div>
        </div>

        {error() && (
          <div style={{ background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', padding: '10px 14px', 'margin-bottom': '18px', color: 'var(--danger)', 'font-size': '13px' }}>
            {error()}
          </div>
        )}

        {!totpRequired() ? (
          <div style={{ display: 'flex', 'flex-direction': 'column', gap: '15px' }}>
            <div>
              <label style={labelStyle}>E-Mail or Username</label>
              <input type="text" value={identifier()} onInput={e => setIdentifier(e.currentTarget.value)} onKeyDown={onKeyDown} autocomplete="username" style={inputStyle} />
            </div>
            <div>
              <label style={labelStyle}>Password</label>
              <div style={{ position: 'relative' }}>
                <input type={showPw() ? 'text' : 'password'} value={password()} onInput={e => setPassword(e.currentTarget.value)} onKeyDown={onKeyDown} autocomplete="current-password" style={{ ...inputStyle, padding: '11px 40px 11px 14px' }} />
                <button type="button" onClick={() => setShowPw(v => !v)} style={{ position: 'absolute', right: '12px', top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', display: 'flex', 'align-items': 'center', padding: '0' }}>
                  {showPw()
                    ? <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                    : <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
                  }
                </button>
              </div>
            </div>
            <label style={{ display: 'flex', 'align-items': 'center', gap: '9px', cursor: 'pointer', 'font-size': '13px', color: 'var(--muted)' }}>
              <input type="checkbox" checked={remember()} onChange={e => setRemember(e.currentTarget.checked)} class="remember-cb" />
              Remember me
            </label>
            <button onClick={handleSubmit} disabled={loading()} class="login-btn" style={{ 'margin-top': '4px', padding: '13px', background: loading() ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '11px', color: '#fff', 'font-size': '15px', 'font-weight': '700', cursor: loading() ? 'not-allowed' : 'pointer', display: 'flex', 'align-items': 'center', 'justify-content': 'center', gap: '9px' }}>
              {loading() ? <><div style={{ width: '17px', height: '17px', border: '2px solid rgba(255,255,255,0.3)', 'border-top': '2px solid #fff', 'border-radius': '50%', animation: 'spin 0.8s linear infinite' }} />Signing in…</> : 'Sign in'}
            </button>
          </div>
        ) : (
          <div style={{ display: 'flex', 'flex-direction': 'column', gap: '15px' }}>
            <div style={{ 'font-size': '13px', color: 'var(--muted)', 'text-align': 'center', 'line-height': '1.5' }}>
              {translate('login.totp_hint') || 'Enter the 6-digit code from your authenticator app.'}
            </div>
            <div>
              <label style={labelStyle}>{translate('login.totp_code') || 'Authenticator code'}</label>
              <input
                type="text"
                inputmode="numeric"
                autocomplete="one-time-code"
                maxlength="6"
                value={totpCode()}
                onInput={e => setTotpCode(e.currentTarget.value.replace(/\D/g, ''))}
                onKeyDown={onKeyDown}
                style={{ ...inputStyle, 'font-size': '22px', 'letter-spacing': '0.3em', 'text-align': 'center' }}
              />
            </div>
            <button onClick={handleTotpSubmit} disabled={loading() || totpCode().length !== 6} class="login-btn" style={{ padding: '13px', background: (loading() || totpCode().length !== 6) ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '11px', color: '#fff', 'font-size': '15px', 'font-weight': '700', cursor: (loading() || totpCode().length !== 6) ? 'not-allowed' : 'pointer', display: 'flex', 'align-items': 'center', 'justify-content': 'center', gap: '9px' }}>
              {loading() ? <><div style={{ width: '17px', height: '17px', border: '2px solid rgba(255,255,255,0.3)', 'border-top': '2px solid #fff', 'border-radius': '50%', animation: 'spin 0.8s linear infinite' }} />Verifying…</> : (translate('login.totp_verify') || 'Verify')}
            </button>
            <button type="button" onClick={() => { setTotpRequired(false); setPendingToken(''); setTotpCode(''); setError('') }} style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', 'font-size': '13px', 'text-decoration': 'underline' }}>
              ← {translate('login.back') || 'Back'}
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
