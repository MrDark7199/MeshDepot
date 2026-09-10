import { sansFont, monoFont } from '../styles/formStyles'
import { Show } from 'solid-js'
import { makeTranslateError } from '../utils/authError'
import type { DesignPageProps } from '../components/DesignPage'

export interface DesignSyncBannerDeps {
  props: DesignPageProps
  translate: (key: any, params?: any) => string
}

/** The strip that reports a running or finished update check. */
export function designSyncBanner(deps: DesignSyncBannerDeps) {
  const { props, translate } = deps
  const translateSyncError = makeTranslateError(translate)
  return (
    <>
      {/* Sync status banner */}
      <Show when={props.syncStatus === 'queued' || props.syncStatus === 'syncing' || props.syncStatus === 'updated' || props.syncStatus === 'error'}>
        <div style={{
          background: props.syncStatus === 'error' ? 'rgba(239,68,68,0.1)' : props.syncStatus === 'updated' ? 'rgba(34,197,94,0.1)' : 'rgba(69,123,157,0.1)',
          'border-bottom': `1px solid ${props.syncStatus === 'error' ? 'rgba(239,68,68,0.3)' : props.syncStatus === 'updated' ? 'rgba(34,197,94,0.3)' : 'rgba(69,123,157,0.3)'}`,
          padding: '10px 36px', display: 'flex', 'align-items': 'center', gap: '10px'
        }}>
          <Show when={props.syncStatus === 'queued' || props.syncStatus === 'syncing'}>
            <div style={{ width: '14px', height: '14px', 'flex-shrink': '0', border: '2px solid var(--accent)', 'border-top-color': 'transparent', 'border-radius': '50%', animation: 'spin 0.7s linear infinite' }} />
          </Show>
          <Show when={props.syncStatus === 'updated'}>
            <div style={{ width: '14px', height: '14px', 'flex-shrink': '0', background: '#22c55e', 'border-radius': '50%', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '9px', color: '#fff' }}>✓</div>
          </Show>
          <Show when={props.syncStatus === 'error'}>
            <div style={{ width: '14px', height: '14px', 'flex-shrink': '0', background: '#ef4444', 'border-radius': '50%', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'font-size': '9px', color: '#fff' }}>✕</div>
          </Show>
          <span style={{ ...sansFont, 'font-size': '13px', color: 'var(--text)', 'font-weight': '500' }}>
            {props.syncStatus === 'queued'  && translate('sync_status_queued')}
            {props.syncStatus === 'syncing' && (
              props.syncStep?.step
                ? (props.syncStep.tot > 0
                    ? `${translate(`sync_step_${props.syncStep.step}` as any)} (${props.syncStep.cur}/${props.syncStep.tot})`
                    : translate(`sync_step_${props.syncStep.step}` as any))
                : (props.syncProgress && props.syncProgress > 0
                    ? `${translate('sync_status_syncing')} ${props.syncProgress}%`
                    : translate('sync_status_syncing'))
            )}
            {props.syncStatus === 'updated' && translate('sync_status_updated')}
            {props.syncStatus === 'error'   && (translateSyncError(props.syncError) || translate('sync_status_error'))}
          </span>
        </div>
      </Show>
    </>
  )
}
