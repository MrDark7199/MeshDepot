import { on } from 'solid-js'

export const globalStyles = `
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  html, body, #root { background: var(--bg); min-height: 100vh; font-size: 16px; -webkit-font-smoothing: antialiased; }
  /* Firefox ignores the ::-webkit- rules and would use the system theme; these
     two are the standard equivalents. Transparent track, so nothing is drawn
     when there is nothing to scroll. Kept in step with index.html. */
  html { scrollbar-width: thin; scrollbar-color: var(--scrollbar) transparent; }
  ::-webkit-scrollbar { width: 8px; }
  ::-webkit-scrollbar-track { background: var(--bg2); }
  ::-webkit-scrollbar-thumb { background: var(--scrollbar); border-radius: 5px; }
  html[data-theme="dark"]  input, html[data-theme="dark"]  select, html[data-theme="dark"]  textarea { color-scheme: dark; }
  html[data-theme="light"] input, html[data-theme="light"] select, html[data-theme="light"] textarea { color-scheme: light; }
  /* 3D-viewer number fields sit in an always-dark panel; give the number a little air before the spinner. */
  .stlv-num::-webkit-inner-spin-button, .stlv-num::-webkit-outer-spin-button { margin-left: 7px; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @keyframes toastIn { from { opacity: 0; transform: translateX(-50%) translateY(-12px); } to { opacity: 1; transform: translateX(-50%) translateY(0); } }

  /* - Mobile -------------------------------- */
  @media (max-width: 640px) {
    /* Nav: wrap to two rows (logo+buttons / search) */
    .stlv-nav { flex-wrap: wrap !important; height: auto !important; padding: 10px 14px !important; column-gap: 10px !important; row-gap: 0 !important; }
    .stlv-nav-logo { flex: 0 0 auto !important; }
    .stlv-nav-right { flex: 0 0 auto !important; margin-left: auto !important; gap: 8px !important; }
    /* Search row drops below logo row */
    .stlv-nav-center { flex: 0 0 100% !important; padding-bottom: 8px !important; }
    .stlv-nav-center .stlv-search { width: 100% !important; }
    /* Hide secondary nav actions on mobile */
    .stlv-nav-extra { display: none !important; }
    /* Grid */
    .stlv-main { padding: 16px 10px !important; }
    .stlv-grid { gap: 12px !important; }
    .stlv-card { width: calc(50vw - 19px) !important; min-width: 140px !important; }
    .stlv-card-cover { width: 100% !important; }
    /* Detail page: single column */
    .stlv-detail-grid { grid-template-columns: 1fr !important; }
    /* Breadcrumb: don't stick below a variable-height nav */
    .stlv-breadcrumb { position: static !important; top: auto !important; }
    /* Notification panel: respect screen edge */
    .stlv-notif-panel { width: min(320px, calc(100vw - 16px)) !important; right: -8px !important; }
    /* Modals */
    .stlv-modal { width: calc(100vw - 24px) !important; padding: 20px !important; }
    .stlv-modal-wide { width: 100vw !important; height: 100dvh !important; border-radius: 0 !important; }
  }
  @media (max-width: 400px) {
    .stlv-card { width: calc(100vw - 20px) !important; }
    .stlv-card-cover { height: 220px !important; }
  }
`
