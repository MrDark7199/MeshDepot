import { browserHandlesClick, gridHref } from '../utils/navlink'
import { makeTranslateError } from '../utils/authError'
import { sansFont, monoFont } from '../styles/formStyles'
import { NAV_H, PAGE_X } from '../constants/layout'
import { Show } from 'solid-js'
import type { Setter } from 'solid-js'
import type { Design } from '../types'
import type { DesignPageProps } from '../components/DesignPage'

export interface DesignBreadcrumbDeps {
  props: DesignPageProps
  translate: (key: any, params?: any) => string
  design: () => Design | null
  shownName: () => string
  isLoading: () => boolean
  enterEditMode: () => void
  openUploadVersion: () => void
  guardClose: (action: () => void) => void
  setConfirmDelete: Setter<boolean>
  startSyncStream: () => void
}

/**
 * The one bar above a design: the way home, the way back, the design's name and
 * what can be done to it, and on the right the two things that are offered
 * everywhere.
 *
 * It replaces the navigation bar on this page rather than standing under it.
 * Two sticky bars came to 141px of frame before the design began, and most of
 * what the upper one offered - filter, grid or list, add a design, sync the
 * library - belongs to the grid and did nothing here.
 */
export function designBreadcrumb(deps: DesignBreadcrumbDeps) {
  const { props, translate, design, shownName, isLoading, enterEditMode, openUploadVersion,
    guardClose, setConfirmDelete, startSyncStream } = deps
  const translateSyncBlock = makeTranslateError(translate)
  const divider = () => <div style={{ height: '25px', width: '1px', background: 'var(--border)', 'flex-shrink': '0' }} />
  return (
    <>
      {/* Gone once the design turned out to be unavailable: with no name and no
          actions left, all it still carried was a second way back, and the error
          panel already offers one. */}
      <Show when={design() || isLoading()}>
      <div class="stlv-breadcrumb" style={{ background: 'var(--nav-bg)', 'backdrop-filter': 'blur(16px)', 'border-bottom': '1px solid var(--border)', padding: `0 ${PAGE_X}`, height: NAV_H, display: 'flex', 'align-items': 'center', gap: '14px', position: 'sticky', top: '0', 'z-index': '100' }}>
        <Show when={props.chrome}>
          {props.chrome!.logo()}
          {divider()}
        </Show>
        {/* An anchor for the same reason as the logo: middle click opens the
            grid in a new tab instead of doing nothing. A left click still runs
            props.onBack, which walks history back to wherever this design was
            opened from - a collection, say, which has no URL of its own. */}
        <a href={gridHref()}
          onClick={event => {
            if (browserHandlesClick(event)) return
            event.preventDefault()
            guardClose(props.onBack)
          }}
          style={{ background: 'none', border: 'none', color: 'var(--text2)', cursor: 'pointer', ...sansFont, 'font-size': '15px', 'font-weight': '600', display: 'flex', 'align-items': 'center', gap: '5px', 'text-decoration': 'none', 'flex-shrink': '0' }}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="15 18 9 12 15 6"/></svg>
          {translate('btn_back')}
        </a>
        <span style={{ ...sansFont, 'font-size': '16px', 'font-weight': '600', color: 'var(--text)', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap', flex: '1', 'min-width': '60px' }}>
          {/* The ellipsis means "still loading"; a design that will never arrive
              must not keep pretending it is on its way. */}
          {design() ? shownName() : isLoading() ? '…' : ''}
        </span>
        <Show when={design()?.is_shared}>
          <span style={{ ...monoFont, 'font-size': '12px', background: 'rgba(69,123,157,0.2)', color: 'var(--accent-light)', 'border-radius': '5px', padding: '3px 9px', 'flex-shrink': '0' }}>
            {translate('shared_with_you')}
          </span>
        </Show>
        {/* Every action here operates on the design; with none loaded they were
            offered anyway, so a design the user may not see still showed
            "delete" and "edit" next to an empty page. */}
        <div style={{ display: 'flex', gap: '8px', 'flex-shrink': '0' }}>
          <Show when={!props.isReadOnly && design()?.source_url}>
            {(() => {
              const active = props.syncStatus === 'queued' || props.syncStatus === 'syncing'
              const blocked = () => design()?.sync_blocked_reason || ''
              const disabled = () => active || !!blocked()
              return (
                <button onClick={disabled() ? undefined : startSyncStream} disabled={disabled()}
                  title={blocked() ? translateSyncBlock(blocked()) : undefined}
                  style={{ padding: '8px 16px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '9px', color: disabled() ? 'var(--muted)' : 'var(--text)', 'font-size': '14px', cursor: disabled() ? 'default' : 'pointer', ...sansFont, 'font-weight': '500', display: 'flex', 'align-items': 'center', gap: '7px', 'white-space': 'nowrap', opacity: disabled() ? '0.7' : '1' }}>
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
              style={{ padding: '8px 16px', background: 'var(--success-bg)', border: '1px solid var(--success-border)', 'border-radius': '9px', color: 'var(--success)', 'font-size': '14px', cursor: 'pointer', ...sansFont, 'font-weight': '600', 'white-space': 'nowrap' }}>
              {translate('btn_upload_version')}
            </button>
            {/* Editing happens on a screen of its own, which this bar makes way
                for - so there is nothing to cancel from here. */}
            <button onClick={enterEditMode}
              style={{ padding: '8px 16px', background: 'var(--accent)', border: '1px solid var(--accent)', 'border-radius': '9px', color: '#fff', 'font-size': '14px', cursor: 'pointer', ...sansFont, 'font-weight': '600' }}>
              {translate('btn_edit')}
            </button>
            <button onClick={() => setConfirmDelete(true)}
              style={{ padding: '8px 14px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '9px', color: 'var(--danger)', 'font-size': '14px', cursor: 'pointer', ...sansFont }}>
              {translate('btn_delete_design')}
            </button>
          </Show>
        </div>
        <Show when={props.chrome}>
          {divider()}
          <div style={{ display: 'flex', 'align-items': 'center', gap: '10px', 'flex-shrink': '0' }}>
            {props.chrome!.globalActions()}
          </div>
        </Show>
      </div>
      </Show>
    </>
  )
}
