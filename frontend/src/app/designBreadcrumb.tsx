import { browserHandlesClick, gridHref } from '../utils/navlink'
import { makeTranslateError } from '../utils/authError'
import { sansFont, monoFont } from '../styles/formStyles'
import { For, Show } from 'solid-js'
import type { Setter } from 'solid-js'
import type { Design } from '../types'
import type { DesignPageProps } from '../components/DesignPage'

export interface DesignBreadcrumbDeps {
  props: DesignPageProps
  translate: (key: any, params?: any) => string
  user: () => any
  design: () => Design | null
  shownName: () => string
  isLoading: () => boolean
  isEditing: () => boolean
  enterEditMode: () => void
  openUploadVersion: () => void
  exitEditMode: () => void
  guardClose: (action: () => void) => void
  setConfirmDelete: Setter<boolean>
  startSyncStream: () => void
}

/** The bar above the design: back link, name, and the actions on the design. */
export function designBreadcrumb(deps: DesignBreadcrumbDeps) {
  const { props, translate, user, design, shownName, isLoading, isEditing, enterEditMode, openUploadVersion,
    exitEditMode, guardClose, setConfirmDelete, startSyncStream } = deps
  const translateSyncBlock = makeTranslateError(translate)
  return (
    <>
      {/* Breadcrumb bar. Gone once the design turned out to be unavailable:
          with no name and no actions left, all it still carried was a second
          way back, and the error panel already offers one. */}
      <Show when={design() || isLoading()}>
      <div class="stlv-breadcrumb" style={{ background: 'var(--nav-bg)', 'backdrop-filter': 'blur(16px)', 'border-bottom': '1px solid var(--border)', padding: '0 36px', height: '56px', display: 'flex', 'align-items': 'center', gap: '16px', position: 'sticky', top: '85px', 'z-index': '90' }}>
        {/* An anchor for the same reason as the nav logo: middle click opens the
            grid in a new tab instead of doing nothing. A left click still runs
            props.onBack, which walks history back to wherever this design was
            opened from - a collection, say, which has no URL of its own. */}
        <a href={gridHref()}
          onClick={event => {
            if (browserHandlesClick(event)) return
            event.preventDefault()
            guardClose(props.onBack)
          }}
          style={{ background: 'none', border: 'none', color: 'var(--text2)', cursor: 'pointer', ...sansFont, 'font-size': '14px', 'font-weight': '600', display: 'flex', 'align-items': 'center', gap: '5px', 'text-decoration': 'none' }}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 18 9 12 15 6"/></svg>
          {translate('btn_back')}
        </a>
        <div style={{ height: '24px', width: '1px', background: 'var(--border)' }} />
        <span style={{ ...sansFont, 'font-size': '14px', color: 'var(--text2)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap', flex: '1' }}>
          {/* The ellipsis means "still loading"; a design that will never arrive
              must not keep pretending it is on its way. */}
          {design() ? shownName() : isLoading() ? '…' : ''}
        </span>
        <Show when={design()?.is_shared}>
          <span style={{ ...monoFont, 'font-size': '11px', background: 'rgba(69,123,157,0.2)', color: 'var(--accent-light)', 'border-radius': '5px', padding: '3px 9px' }}>
            {translate('shared_with_you')}
          </span>
        </Show>
        {/* Every action here operates on the design; with none loaded they were
            offered anyway, so a design the user may not see still showed
            "delete" and "edit" next to an empty page. */}
        <div style={{ display: 'flex', gap: '8px', 'margin-left': 'auto' }}>
          <Show when={!props.isReadOnly && design()?.source_url}>
            {(() => {
              const active = props.syncStatus === 'queued' || props.syncStatus === 'syncing'
              const blocked = () => design()?.sync_blocked_reason || ''
              const disabled = () => active || !!blocked()
              return (
                <button onClick={disabled() ? undefined : startSyncStream} disabled={disabled()}
                  title={blocked() ? translateSyncBlock(blocked()) : undefined}
                  style={{ padding: '7px 15px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '9px', color: disabled() ? 'var(--muted)' : 'var(--text)', 'font-size': '13px', cursor: disabled() ? 'default' : 'pointer', ...sansFont, 'font-weight': '500', display: 'flex', 'align-items': 'center', gap: '7px', opacity: disabled() ? '0.7' : '1' }}>
                  <Show when={active}>
                    <div style={{ width: '12px', height: '12px', 'flex-shrink': '0', border: '2px solid var(--muted)', 'border-top-color': 'transparent', 'border-radius': '50%', animation: 'spin 0.7s linear infinite' }} />
                  </Show>
                  {active ? translate('btn_syncing') : translate('btn_sync')}
                </button>
              )
            })()}
          </Show>
          <Show when={!props.isReadOnly && design()}>
            <button onClick={openUploadVersion}
              style={{ padding: '7px 15px', background: 'var(--success-bg)', border: '1px solid var(--success-border)', 'border-radius': '9px', color: 'var(--success)', 'font-size': '13px', cursor: 'pointer', ...sansFont, 'font-weight': '600' }}>
              {translate('btn_upload_version')}
            </button>
            <button onClick={() => {
              if (!isEditing()) { enterEditMode() }
              else { guardClose(exitEditMode) }
            }}
              style={{ padding: '7px 15px', background: isEditing() ? 'var(--surface)' : 'var(--accent)', border: `1px solid ${isEditing() ? 'var(--border)' : 'var(--accent)'}`, 'border-radius': '9px', color: isEditing() ? 'var(--muted)' : '#fff', 'font-size': '13px', cursor: 'pointer', ...sansFont, 'font-weight': '600' }}>
              {isEditing() ? translate('btn_cancel') : translate('btn_edit')}
            </button>
            <button onClick={() => setConfirmDelete(true)}
              style={{ padding: '7px 13px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', color: 'var(--danger)', 'font-size': '13px', cursor: 'pointer', ...sansFont }}>
              {translate('btn_delete_design')}
            </button>
          </Show>
        </div>
      </div>
      </Show>
    </>
  )
}
