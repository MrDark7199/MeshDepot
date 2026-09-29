import { For, Show, createSignal, onMount } from 'solid-js'
import { api } from '../../services/api'
import { errorKey } from '../../utils/errorMessage'
import { resetDirty } from '../../utils/unsavedChanges'
import { Card, ConfirmDialog, Err, inp, lbl, mono, sans } from './shared'
import type { CustomField, CustomFieldInput } from '../../types'

const FIELD_TYPES: CustomField['field_type'][] = ['text', 'int', 'float', 'boolean', 'select', 'multiselect']

/** The types whose values are picked from a list the user writes down. */
const CHOOSABLE = new Set(['select', 'multiselect'])

const emptyDraft = (): CustomFieldInput => ({ name: '', field_type: 'text', options: [] })

/**
 * Fields of one's own, for what the built-in columns do not cover. They belong
 * to the account: every design of this user offers them, and a design shared
 * with somebody else carries none of them.
 */
export function TabCustomFields(props: { translate: (key: any, params?: any) => string; showToast: (message: string, kind?: 'error') => void }) {
  const [fields, setFields] = createSignal<CustomField[]>([])
  const [draft, setDraft] = createSignal<CustomFieldInput>(emptyDraft())
  const [editingId, setEditingId] = createSignal<number | null>(null)
  const [adding, setAdding] = createSignal(false)
  const [optionText, setOptionText] = createSignal('')
  const [errorMessage, setErrorMessage] = createSignal('')
  const [busy, setBusy] = createSignal(false)
  const [pendingDelete, setPendingDelete] = createSignal<CustomField | null>(null)

  const load = async () => {
    try {
      // The service hands back the envelope here, as it does for the other tabs.
      const answer = await api.customFields() as any
      setFields(answer?.data ?? [])
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    }
  }
  onMount(load)

  // This tab saves a field on its own button, so the typing in its form is not
  // an unsaved change of the settings dialog. Cleared once the draft is gone,
  // which is either after saving it or after dropping it.
  const closeForm = () => {
    setAdding(false)
    setEditingId(null)
    setDraft(emptyDraft())
    setOptionText('')
    setErrorMessage('')
    resetDirty()
  }

  const startAdd = () => { closeForm(); setAdding(true) }

  const startEdit = (field: CustomField) => {
    closeForm()
    setEditingId(field.id)
    setDraft({ name: field.name, field_type: field.field_type, options: field.options ?? [] })
    setOptionText((field.options ?? []).join('\n'))
  }

  const save = async () => {
    const current = draft()
    // One choice per line is the least fiddly way to type a handful of them.
    const options = CHOOSABLE.has(current.field_type)
      ? optionText().split('\n').map(line => line.trim()).filter(Boolean)
      : []
    setBusy(true)
    setErrorMessage('')
    try {
      const id = editingId()
      if (id === null) await api.createCustomField({ ...current, options })
      else await api.updateCustomField(id, { ...current, options })
      closeForm()
      await load()
    } catch (failure: unknown) {
      setErrorMessage(props.translate(errorKey(failure)))
    } finally {
      setBusy(false)
    }
  }

  const remove = async (field: CustomField) => {
    try {
      await api.deleteCustomField(field.id)
      resetDirty()
      await load()
    } catch (failure: unknown) {
      props.showToast(props.translate(errorKey(failure)), 'error')
    }
  }

  const form = () => (
    <Card>
      <Err message={errorMessage()} />
      <div style={{ display: 'flex', 'flex-direction': 'column', gap: '12px' }}>
        <div>
          <label style={lbl}>{props.translate('field_name')} *</label>
          <input style={inp} value={draft().name} placeholder={props.translate('custom_field_name_hint')}
            onInput={event => setDraft(current => ({ ...current, name: event.currentTarget.value }))} />
        </div>
        <div>
          <label style={lbl}>{props.translate('custom_field_type')}</label>
          <select style={inp} value={draft().field_type}
            onChange={event => setDraft(current => ({ ...current, field_type: event.currentTarget.value as CustomField['field_type'] }))}>
            <For each={FIELD_TYPES}>{type => (
              <option value={type}>{props.translate(`custom_field_type_${type}` as any)}</option>
            )}</For>
          </select>
        </div>
        <Show when={CHOOSABLE.has(draft().field_type)}>
          <div>
            <label style={lbl}>{props.translate('custom_field_options')} *</label>
            <textarea rows={4} style={{ ...inp, resize: 'vertical' }} value={optionText()}
              placeholder={props.translate('custom_field_options_hint')}
              onInput={event => setOptionText(event.currentTarget.value)} />
          </div>
        </Show>
        <div style={{ display: 'flex', gap: '9px', 'justify-content': 'flex-end' }}>
          <button onClick={closeForm}
            style={{ ...sans, padding: '8px 16px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '9px', color: 'var(--muted)', 'font-size': '13px', cursor: 'pointer' }}>
            {props.translate('btn_cancel')}
          </button>
          <button onClick={save} disabled={busy() || !draft().name.trim()}
            style={{ ...sans, padding: '8px 18px', background: (busy() || !draft().name.trim()) ? 'var(--bg4)' : 'var(--accent)', border: 'none', 'border-radius': '9px', color: (busy() || !draft().name.trim()) ? 'var(--muted)' : '#fff', 'font-size': '13px', 'font-weight': '700', cursor: (busy() || !draft().name.trim()) ? 'not-allowed' : 'pointer' }}>
            {props.translate('btn_save')}
          </button>
        </div>
      </div>
    </Card>
  )

  return (
    <div style={{ display: 'flex', 'flex-direction': 'column', gap: '14px' }}>
      <div style={{ ...sans, 'font-size': '12px', color: 'var(--muted)', 'line-height': '1.6' }}>
        {props.translate('custom_fields_hint')}
      </div>

      <Show when={fields().length === 0 && !adding()}>
        <div style={{ ...sans, 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic' }}>
          {props.translate('custom_fields_none')}
        </div>
      </Show>

      <For each={fields()}>{field => (
        <Show when={editingId() === field.id} fallback={
          <Card>
            <div style={{ display: 'flex', 'align-items': 'center', gap: '12px' }}>
              <div style={{ flex: '1', 'min-width': '0' }}>
                <div style={{ ...sans, 'font-size': '14px', 'font-weight': '600', color: 'var(--text)' }}>{field.name}</div>
                <div style={{ ...mono, 'font-size': '11px', color: 'var(--muted)', 'margin-top': '3px' }}>
                  {props.translate(`custom_field_type_${field.field_type}` as any)}
                  <Show when={CHOOSABLE.has(field.field_type) && field.options?.length}>
                    {' · ' + (field.options ?? []).join(', ')}
                  </Show>
                  <Show when={(field.usage_count ?? 0) > 0}>
                    {' · ' + props.translate('custom_field_in_use').replace('{count}', String(field.usage_count))}
                  </Show>
                </div>
              </div>
              <button onClick={() => startEdit(field)}
                style={{ ...sans, padding: '6px 13px', background: 'var(--bg4)', border: '1px solid var(--border2)', 'border-radius': '8px', color: 'var(--text)', 'font-size': '12px', cursor: 'pointer' }}>
                {props.translate('btn_edit')}
              </button>
              <button onClick={() => setPendingDelete(field)} title={props.translate('custom_field_delete_hint')}
                style={{ ...sans, padding: '6px 13px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', color: 'var(--danger)', 'font-size': '12px', cursor: 'pointer' }}>
                {props.translate('btn_delete')}
              </button>
            </div>
          </Card>
        }>
          {form()}
        </Show>
      )}</For>

      <Show when={pendingDelete()}>
        {field => (
          <ConfirmDialog
            title={props.translate('custom_field_delete_title')}
            body={(field().usage_count ?? 0) > 0
              ? props.translate('custom_field_delete_body_used')
                  .replace('{name}', field().name)
                  .replace('{count}', String(field().usage_count))
              : props.translate('custom_field_delete_body').replace('{name}', field().name)}
            confirmLabel={props.translate('btn_confirm_delete')}
            cancelLabel={props.translate('btn_cancel')}
            onCancel={() => setPendingDelete(null)}
            onConfirm={() => { const doomed = field(); setPendingDelete(null); remove(doomed) }} />
        )}
      </Show>

      <Show when={adding()} fallback={
        <button onClick={startAdd}
          style={{ ...sans, 'align-self': 'flex-start', padding: '9px 18px', background: 'var(--accent)', border: 'none', 'border-radius': '9px', color: '#fff', 'font-size': '13px', 'font-weight': '700', cursor: 'pointer' }}>
          ＋ {props.translate('custom_field_add')}
        </button>
      }>
        {form()}
      </Show>
    </div>
  )
}
