import { For, Show, createSignal } from 'solid-js'
import type { JSX, Setter } from 'solid-js'
import { api } from '../services/api'
import { ErrorBox } from '../components/ErrorBox'
import { StarRating } from '../components/StarRating'
import { ToggleSwitch } from '../components/ToggleSwitch'
import { parseChoices, serialiseChoices } from '../utils/customFieldValue'
import { sansFont, monoFont, labelStyle, inputStyle, TAG_COLOR_PRESETS } from '../styles/formStyles'
import { NAV_H, PAGE_X } from '../constants/layout'
import type { Collection, CustomField, Tag } from '../types'
import type { DesignEditForm } from '../components/DesignPage'

export interface DesignEditScreenDeps {
  translate: (key: any, params?: any) => string
  /** The same logo the other bars carry. It asks before leaving: the guard sees
   *  the open form, so an unsaved change is not lost to a stray click. */
  logo?: () => JSX.Element
  /** The design's name as the detail view shows it, for the bar. */
  shownName: () => string
  editForm: () => DesignEditForm
  setEditForm: Setter<DesignEditForm>
  errorMessage: () => string
  isSaving: () => boolean
  saveDesign: () => void
  /** Leaves the screen, asking first when something was typed. */
  cancelEdit: () => void

  // Images, cover and deletions alike: nothing reaches the server before Save.
  galleryImages: () => { id: number; path: string }[]
  effectiveCoverId: () => number | null
  stageCover: (imageId: number) => void
  isStagedDelete: (imageId: number) => boolean
  toggleDeleteImage: (imageId: number) => void
  /** Photos picked here, shown from a local preview until they are uploaded.
   *  The key stands in for the id they do not have yet, so one of them can be
   *  the chosen cover. */
  pendingImages: () => { key: number; url: string }[]
  addPendingImages: (files: FileList | File[]) => void
  removePendingImage: (index: number) => void

  // Tags: searched on the server, picked ones held as chips.
  selectedTags: () => Tag[]
  notSelected: (tag: Tag) => boolean
  addSelectedTag: (tag: Tag) => void
  removeSelectedTag: (tagId: number) => void
  tagQuery: () => string
  onTagInput: (value: string) => void
  runTagSearch: (value: string) => void
  tagResults: () => Tag[]
  tagOpen: () => boolean
  setTagOpen: Setter<boolean>
  onTagKeyDown: (event: KeyboardEvent) => void
  showCreateTag: () => boolean
  createNewTag: () => void
  newTagColor: () => string
  setNewTagColor: Setter<string>

  // Collections: filtered on the client, and persisted as they are picked.
  selectedCollections: () => Collection[]
  colResults: () => Collection[]
  addCollectionSel: (collection: Collection) => void
  removeCollectionSel: (collectionId: number) => void
  colQuery: () => string
  setColQuery: Setter<string>
  colOpen: () => boolean
  setColOpen: Setter<boolean>
  onColKeyDown: (event: KeyboardEvent) => void
  showCreateCol: () => boolean
  createNewCollection: () => void

  // The reader's own fields, and the values this design carries.
  customFields: () => CustomField[]
  customValues: () => Record<string, string>
  setCustomValues: Setter<Record<string, string>>
}

const cardStyle: JSX.CSSProperties = { background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '16px', padding: '20px' }
const cardTitleStyle: JSX.CSSProperties = { ...monoFont, 'font-size': '11px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.08em', 'margin-bottom': '16px', 'font-weight': '600' }
const hintStyle: JSX.CSSProperties = { ...sansFont, 'font-size': '11px', color: 'var(--muted)', 'line-height': '1.6', 'margin-top': '8px' }
const columnStyle: JSX.CSSProperties = { display: 'flex', 'flex-direction': 'column', gap: '18px' }
const switchLabelStyle: JSX.CSSProperties = { ...sansFont, 'font-size': '13px', color: 'var(--text2)' }
const emptyStyle: JSX.CSSProperties = { ...sansFont, 'font-size': '13px', color: 'var(--muted)', 'font-style': 'italic' }
const dropdownStyle: JSX.CSSProperties = { position: 'absolute', top: 'calc(100% + 4px)', left: '0', right: '0', 'z-index': '30', background: 'var(--bg2)', border: '1px solid var(--border)', 'border-radius': '10px', 'box-shadow': '0 12px 30px rgba(0,0,0,0.35)', 'max-height': '240px', 'overflow-y': 'auto', padding: '5px' }
const optionStyle: JSX.CSSProperties = { display: 'flex', 'align-items': 'center', gap: '8px', width: '100%', 'text-align': 'left', padding: '7px 9px', background: 'none', border: 'none', 'border-radius': '7px', cursor: 'pointer', ...sansFont, 'font-size': '13px', color: 'var(--text2)' }
const createButtonStyle: JSX.CSSProperties = { 'max-width': '100%', padding: '5px 11px', background: 'var(--accent)', border: 'none', 'border-radius': '7px', color: '#fff', 'font-size': '13px', cursor: 'pointer', ...sansFont, overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }

/**
 * The screen a design is edited on - it takes the place of the detail view
 * rather than sitting below it.
 *
 * Only what is actually typed is here: files, versions, print parameters,
 * shares, size and dates are not edited, so they stay on the detail view the
 * two buttons in the bar lead back to. The left column holds the images and the
 * filing, the right one the text, which keeps either from becoming a tower.
 */
export function designEditScreen(deps: DesignEditScreenDeps) {
  const { translate, logo, shownName, editForm, setEditForm, errorMessage, isSaving, saveDesign,
    cancelEdit, galleryImages, effectiveCoverId, stageCover, isStagedDelete, toggleDeleteImage,
    pendingImages, addPendingImages, removePendingImage, selectedTags, notSelected, addSelectedTag,
    removeSelectedTag, tagQuery, onTagInput, runTagSearch, tagResults, tagOpen, setTagOpen, onTagKeyDown,
    showCreateTag, createNewTag, newTagColor, setNewTagColor, selectedCollections, colResults,
    addCollectionSel, removeCollectionSel, colQuery, setColQuery, colOpen, setColOpen, onColKeyDown,
    showCreateCol, createNewCollection, customFields, customValues, setCustomValues } = deps

  const [isDraggingImage, setIsDraggingImage] = createSignal(false)
  let imageInput: HTMLInputElement | undefined

  const setField = (changes: Partial<DesignEditForm>) => setEditForm(form => ({ ...form, ...changes }))

  /** Fields put on this design, in the order the user defined them. */
  const fieldsOnDesign = () => customFields().filter(field => String(field.id) in customValues())
  const fieldsOffDesign = () => customFields().filter(field => !(String(field.id) in customValues()))

  // No height of its own: App and the design page already paint a full-height
  // background, and a third 100vh in the stack only guaranteed a scrollbar.
  return (
    <div>
      {/* A media query cannot be written inline, and one column below 1100px is
          what keeps the two from being squeezed into unreadable strips. The pair
          is centred rather than stretched: a text field as wide as a monitor is
          harder to read, not easier. */}
      <style>{`
        .design-edit-body { display: grid; grid-template-columns: minmax(340px, 560px) minmax(0, 856px);
                            justify-content: center; gap: 24px; padding: 24px ${PAGE_X} 36px;
                            max-width: 1464px; margin: 0 auto; }
        @media (max-width: 1100px) { .design-edit-body { grid-template-columns: minmax(0, 860px);
                                                         max-width: 916px; } }
      `}</style>

      {/* Built like the bar above a design - same height, same margin, same logo
          and divider, the name in the same place - so the screen reads as the
          page it was opened from. What differs is the right half: a task offers
          its two exits where the design offers what can be done to it. */}
      <div style={{ position: 'sticky', top: '0', 'z-index': '90', display: 'flex', 'align-items': 'center', gap: '14px', padding: `0 ${PAGE_X}`, height: NAV_H, background: 'var(--nav-bg)', 'backdrop-filter': 'blur(16px)', 'border-bottom': '1px solid var(--border)' }}>
        <Show when={logo}>
          {logo!()}
          <div style={{ height: '25px', width: '1px', background: 'var(--border)', 'flex-shrink': '0' }} />
        </Show>
        <span style={{ ...monoFont, 'font-size': '12px', color: 'var(--muted)', 'text-transform': 'uppercase', 'letter-spacing': '0.08em', 'flex-shrink': '0' }}>
          {translate('label_edit_design')}
        </span>
        <span style={{ ...sansFont, 'font-size': '16px', 'font-weight': '600', color: 'var(--text)', flex: '1', 'min-width': '60px', overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' }}>
          {shownName()}
        </span>
        <div style={{ display: 'flex', gap: '8px', 'flex-shrink': '0' }}>
          <button onClick={cancelEdit}
            style={{ padding: '8px 16px', background: 'var(--surface)', border: '1px solid var(--border)', 'border-radius': '9px', color: 'var(--text3)', ...sansFont, 'font-size': '14px', 'font-weight': '600', cursor: 'pointer' }}>
            {translate('btn_cancel')}
          </button>
          <button onClick={saveDesign} disabled={isSaving()}
            style={{ padding: '8px 16px', background: isSaving() ? 'var(--bg4)' : 'var(--accent)', border: `1px solid ${isSaving() ? 'var(--border2)' : 'var(--accent)'}`, 'border-radius': '9px', color: isSaving() ? 'var(--muted)' : '#fff', ...sansFont, 'font-size': '14px', 'font-weight': '600', cursor: isSaving() ? 'not-allowed' : 'pointer' }}>
            {isSaving() ? translate('btn_saving') : translate('btn_save')}
          </button>
        </div>
      </div>

      <div class="design-edit-body">

        {/* - Left: images and where the design is filed - */}
        <div style={columnStyle}>

          <div style={cardStyle}>
            <div style={cardTitleStyle}>{translate('edit_section_images')}</div>
            <Show when={galleryImages().length > 0 || pendingImages().length > 0} fallback={
              <div style={emptyStyle}>{translate('edit_images_none')}</div>
            }>
              <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fill, minmax(120px, 1fr))', gap: '10px' }}>
                <For each={galleryImages()}>{image => {
                  const doomed = () => isStagedDelete(image.id)
                  const isCover = () => effectiveCoverId() === image.id
                  // id 0 is the cover_path fallback - a picture that is not a
                  // gallery row, so it can be neither recovered nor deleted here.
                  const stageable = () => image.id > 0
                  return (
                    <div onClick={() => { if (stageable() && !doomed()) stageCover(image.id) }}
                      title={stageable() && !doomed() ? translate('btn_set_cover') : undefined}
                      style={{ position: 'relative', 'aspect-ratio': '4 / 3', 'border-radius': '10px', overflow: 'hidden', cursor: stageable() && !doomed() ? 'pointer' : 'default', border: `2px solid ${doomed() ? 'var(--danger)' : isCover() ? 'var(--accent)' : 'transparent'}` }}>
                      <img src={api.coverUrl(image.path)} alt=""
                        style={{ width: '100%', height: '100%', 'object-fit': 'cover', opacity: doomed() ? '0.3' : '1' }} />
                      <Show when={isCover() && !doomed()}>
                        <span style={{ position: 'absolute', left: '6px', bottom: '6px', ...monoFont, 'font-size': '10px', padding: '2px 7px', 'border-radius': '6px', background: 'rgba(0,0,0,0.65)', color: 'var(--accent-light)' }}>
                          {translate('label_cover')}
                        </span>
                      </Show>
                      <Show when={stageable()}>
                        <Show when={doomed()} fallback={
                          <button onClick={event => { event.stopPropagation(); toggleDeleteImage(image.id) }}
                            title={translate('btn_delete_image')}
                            style={{ position: 'absolute', right: '5px', top: '5px', width: '24px', height: '24px', padding: '0', 'border-radius': '7px', background: 'rgba(230,57,70,0.92)', border: 'none', color: '#fff', cursor: 'pointer', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
                              <polyline points="3 6 5 6 21 6" />
                              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                            </svg>
                          </button>
                        }>
                          <button onClick={event => { event.stopPropagation(); toggleDeleteImage(image.id) }}
                            style={{ position: 'absolute', inset: '0', background: 'rgba(230,57,70,0.2)', border: 'none', color: 'var(--danger)', cursor: 'pointer', ...monoFont, 'font-size': '11px', 'font-weight': '700' }}>
                            {translate('btn_undo_delete')}
                          </button>
                        </Show>
                      </Show>
                    </div>
                  )
                }}</For>
                <For each={pendingImages()}>{(picked, index) => {
                  const isCover = () => effectiveCoverId() === picked.key
                  return (
                  <div onClick={() => stageCover(picked.key)} title={translate('btn_set_cover')}
                    style={{ position: 'relative', 'aspect-ratio': '4 / 3', 'border-radius': '10px', overflow: 'hidden', cursor: 'pointer', border: `2px dashed ${isCover() ? 'var(--accent)' : 'var(--border2)'}` }}>
                    <img src={picked.url} alt="" style={{ width: '100%', height: '100%', 'object-fit': 'cover' }} />
                    <span style={{ position: 'absolute', left: '6px', bottom: '6px', ...monoFont, 'font-size': '10px', padding: '2px 7px', 'border-radius': '6px', background: 'rgba(0,0,0,0.65)', color: isCover() ? 'var(--accent-light)' : 'var(--muted)' }}>
                      {isCover() ? translate('label_cover') : translate('edit_image_pending')}
                    </span>
                    <button onClick={event => { event.stopPropagation(); removePendingImage(index()) }} title={translate('btn_remove')}
                      style={{ position: 'absolute', right: '5px', top: '5px', width: '24px', height: '24px', padding: '0', 'border-radius': '7px', background: 'rgba(230,57,70,0.92)', border: 'none', color: '#fff', cursor: 'pointer', display: 'flex', 'align-items': 'center', 'justify-content': 'center' }}>
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
                        <polyline points="3 6 5 6 21 6" />
                        <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                      </svg>
                    </button>
                  </div>
                  )
                }}</For>
              </div>
            </Show>

            <div onClick={() => imageInput?.click()}
              onDragOver={event => { event.preventDefault(); setIsDraggingImage(true) }}
              onDragLeave={() => setIsDraggingImage(false)}
              onDrop={event => {
                event.preventDefault()
                setIsDraggingImage(false)
                addPendingImages(event.dataTransfer?.files ?? [])
              }}
              style={{ 'margin-top': galleryImages().length > 0 || pendingImages().length > 0 ? '12px' : '0', border: `2px dashed ${isDraggingImage() ? 'var(--accent)' : 'var(--border2)'}`, 'border-radius': '11px', padding: '18px', 'text-align': 'center', cursor: 'pointer', ...monoFont, 'font-size': '12px', color: 'var(--muted)', background: isDraggingImage() ? 'rgba(69,123,157,0.12)' : 'transparent' }}>
              <input ref={element => (imageInput = element)} type="file" accept="image/*" multiple style={{ display: 'none' }}
                onChange={event => {
                  addPendingImages(event.currentTarget.files ?? [])
                  event.currentTarget.value = ''
                }} />
              {translate('edit_images_drop')}
            </div>
            <div style={hintStyle}>{translate('edit_images_hint')}</div>
          </div>

          <div style={cardStyle}>
            <div style={cardTitleStyle}>{translate('field_visibility')}</div>
            <div style={{ display: 'flex', 'align-items': 'center', gap: '11px' }}>
              <ToggleSwitch checked={!editForm().is_hidden} onChange={shown => setField({ is_hidden: shown ? 0 : 1 })} />
              <span style={{ ...switchLabelStyle, cursor: 'pointer' }}
                onClick={() => setField({ is_hidden: editForm().is_hidden ? 0 : 1 })}>
                {translate('edit_visibility_shown')}
              </span>
            </div>
            <div style={hintStyle}>{translate('edit_visibility_hint')}</div>
          </div>

          <div style={cardStyle}>
            <div style={cardTitleStyle}>{translate('edit_section_filing')}</div>

            <div>
              <label style={labelStyle}>{translate('label_tags')}</label>
              <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '7px', 'margin-bottom': '9px' }}>
                <For each={selectedTags()}>{tag => (
                  <span style={{ display: 'inline-flex', 'align-items': 'center', gap: '6px', 'font-size': '12px', padding: '4px 6px 4px 11px', 'border-radius': '7px', background: tag.color + '33', color: tag.color, ...sansFont, 'font-weight': '600' }}>
                    {tag.name}
                    <button onClick={() => removeSelectedTag(tag.id)} title={translate('btn_delete')}
                      style={{ background: 'none', border: 'none', color: tag.color, cursor: 'pointer', 'font-size': '15px', 'line-height': '1', padding: '0' }}>×</button>
                  </span>
                )}</For>
                <Show when={selectedTags().length === 0}>
                  <span style={emptyStyle}>{translate('label_no_tags')}</span>
                </Show>
              </div>
              <div style={{ position: 'relative' }}>
                <input value={tagQuery()} placeholder={translate('tag_search_placeholder')}
                  onInput={event => onTagInput(event.currentTarget.value)}
                  onFocus={() => { setTagOpen(true); runTagSearch(tagQuery()) }}
                  onClick={() => { if (!tagOpen()) { setTagOpen(true); runTagSearch(tagQuery()) } }}
                  onBlur={() => setTimeout(() => setTagOpen(false), 150)}
                  onKeyDown={onTagKeyDown}
                  style={{ ...inputStyle, width: '100%', 'font-size': '13px', padding: '7px 11px' }} />
                <Show when={tagOpen() && (tagResults().filter(notSelected).length > 0 || showCreateTag())}>
                  <div style={dropdownStyle}>
                    <For each={tagResults().filter(notSelected)}>{tag => (
                      <button onMouseDown={event => event.preventDefault()} onClick={() => addSelectedTag(tag)} style={optionStyle}>
                        <span style={{ width: '10px', height: '10px', 'border-radius': '3px', background: tag.color, 'flex-shrink': '0' }} />
                        {tag.name}
                      </button>
                    )}</For>
                    <Show when={showCreateTag()}>
                      <div style={{ display: 'flex', 'align-items': 'center', gap: '8px', padding: '7px 9px', 'border-top': tagResults().filter(notSelected).length > 0 ? '1px solid var(--border)' : 'none' }}>
                        <div style={{ display: 'flex', gap: '3px', 'flex-shrink': '0' }}>
                          <For each={TAG_COLOR_PRESETS.slice(0, 7)}>{color => (
                            <div onMouseDown={event => event.preventDefault()} onClick={() => setNewTagColor(color)}
                              style={{ width: '18px', height: '18px', 'border-radius': '4px', background: color, cursor: 'pointer', border: newTagColor() === color ? '2px solid #fff' : '2px solid transparent' }} />
                          )}</For>
                        </div>
                        <button onMouseDown={event => event.preventDefault()} onClick={createNewTag}
                          style={{ ...createButtonStyle, 'flex-shrink': '0' }}>
                          {translate('tag_create', { name: tagQuery().trim() })}
                        </button>
                      </div>
                    </Show>
                  </div>
                </Show>
              </div>
            </div>

            <div style={{ 'margin-top': '16px' }}>
              <label style={labelStyle}>{translate('collection_title')}</label>
              <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '7px', 'margin-bottom': '9px' }}>
                <For each={selectedCollections()}>{collection => (
                  <span style={{ display: 'inline-flex', 'align-items': 'center', gap: '6px', 'font-size': '12px', padding: '4px 6px 4px 11px', 'border-radius': '7px', background: 'rgba(69,123,157,0.18)', color: 'var(--accent-light)', ...sansFont, 'font-weight': '600' }}>
                    {collection.name}
                    <button onClick={() => removeCollectionSel(collection.id)} title={translate('btn_delete')}
                      style={{ background: 'none', border: 'none', color: 'var(--accent-light)', cursor: 'pointer', 'font-size': '15px', 'line-height': '1', padding: '0' }}>×</button>
                  </span>
                )}</For>
                <Show when={selectedCollections().length === 0}>
                  <span style={emptyStyle}>{translate('label_no_collections')}</span>
                </Show>
              </div>
              <div style={{ position: 'relative' }}>
                <input value={colQuery()} placeholder={translate('collection_search_placeholder')}
                  onInput={event => { setColQuery(event.currentTarget.value); setColOpen(true) }}
                  onFocus={() => setColOpen(true)}
                  onClick={() => setColOpen(true)}
                  onBlur={() => setTimeout(() => setColOpen(false), 150)}
                  onKeyDown={onColKeyDown}
                  style={{ ...inputStyle, width: '100%', 'font-size': '13px', padding: '7px 11px' }} />
                <Show when={colOpen() && (colResults().length > 0 || showCreateCol())}>
                  <div style={dropdownStyle}>
                    <For each={colResults()}>{collection => (
                      <button onMouseDown={event => event.preventDefault()} onClick={() => addCollectionSel(collection)} style={optionStyle}>
                        {collection.name}
                      </button>
                    )}</For>
                    <Show when={showCreateCol()}>
                      <div style={{ padding: '7px 9px', 'border-top': colResults().length > 0 ? '1px solid var(--border)' : 'none' }}>
                        <button onMouseDown={event => event.preventDefault()} onClick={createNewCollection}
                          style={createButtonStyle}>
                          {translate('collection_create', { name: colQuery().trim() })}
                        </button>
                      </div>
                    </Show>
                  </div>
                </Show>
              </div>
              <div style={hintStyle}>{translate('edit_collections_hint')}</div>
            </div>
          </div>
        </div>

        {/* - Right: everything that is typed - */}
        <div style={columnStyle}>
          <ErrorBox message={errorMessage()} />

          <div style={cardStyle}>
            <div style={cardTitleStyle}>{translate('edit_section_basics')}</div>
            <div>
              <label style={labelStyle}>{translate('field_name')} *</label>
              <input style={inputStyle} value={editForm().name}
                onInput={event => setField({ name: event.currentTarget.value })} />
            </div>
            <div style={{ 'margin-top': '14px' }}>
              <label style={labelStyle}>{translate('field_description')}</label>
              <textarea rows={4} value={editForm().description}
                onInput={event => setField({ description: event.currentTarget.value })}
                style={{ ...inputStyle, resize: 'vertical', 'min-height': '96px', 'line-height': '1.6' }} />
            </div>
          </div>

          <div style={cardStyle}>
            <div style={cardTitleStyle}>{translate('edit_section_facts')}</div>
            <div style={{ display: 'grid', 'grid-template-columns': 'repeat(auto-fit, minmax(170px, 1fr))', gap: '14px' }}>
              <div>
                <label style={labelStyle}>{translate('field_category')}</label>
                <input style={inputStyle} value={editForm().category}
                  onInput={event => setField({ category: event.currentTarget.value })} />
              </div>
              <div>
                <label style={labelStyle}>{translate('field_license')}</label>
                <input style={inputStyle} value={editForm().license}
                  onInput={event => setField({ license: event.currentTarget.value })} />
              </div>
              <div>
                <label style={labelStyle}>{translate('field_author')}</label>
                <input style={inputStyle} value={editForm().author}
                  onInput={event => setField({ author: event.currentTarget.value })} />
              </div>
            </div>
            <div style={{ 'margin-top': '14px' }}>
              <label style={labelStyle}>{translate('field_rating')}</label>
              {/* Saved with the rest here, unlike the stars on the detail view,
                  which write straight through - Cancel has to mean cancel. */}
              <StarRating value={editForm().rating} onChange={rating => setField({ rating })} />
            </div>
            <div style={{ 'margin-top': '14px' }}>
              <label style={labelStyle}>{translate('field_source_url')}</label>
              <input style={inputStyle} value={editForm().source_url}
                onInput={event => setField({ source_url: event.currentTarget.value })} />
              <div style={hintStyle}>{translate('edit_source_hint')}</div>
            </div>
          </div>

          <div style={cardStyle}>
            <div style={cardTitleStyle}>{translate('tab_notes')}</div>
            <textarea rows={5} value={editForm().notes}
              onInput={event => setField({ notes: event.currentTarget.value })}
              placeholder={translate('notes_placeholder')}
              style={{ ...inputStyle, resize: 'vertical', 'min-height': '120px', 'line-height': '1.65' }} />
          </div>

          {/* The user's own fields, and only the ones put on this design: a field
              that is not here is not on the design at all, so a yes/no does not
              read as "no" on everything ever defined. */}
          <Show when={customFields().length > 0}>
            <div style={cardStyle}>
              <div style={cardTitleStyle}>{translate('section_custom_fields')}</div>
              <For each={fieldsOnDesign()}>{(field, index) => {
                const value = () => customValues()[String(field.id)] ?? ''
                const setValue = (next: string) =>
                  setCustomValues(current => ({ ...current, [String(field.id)]: next }))
                const drop = () => setCustomValues(current => {
                  const { [String(field.id)]: _removed, ...rest } = current
                  return rest
                })
                return (
                  <div style={index() === 0
                    ? {}
                    : { 'border-top': '1px solid var(--border)', 'padding-top': '14px', 'margin-top': '14px' }}>
                    {/* The label sits on the baseline of its row, so the gap below
                        belongs to the row rather than to the label alone. */}
                    <div style={{ display: 'flex', 'align-items': 'center', 'justify-content': 'space-between', gap: '10px', 'margin-bottom': '10px' }}>
                      <label style={{ ...labelStyle, 'margin-bottom': '0' }}>{field.name}</label>
                      <button onClick={drop} title={translate('custom_field_remove_hint')}
                        style={{ padding: '5px 12px', background: 'var(--danger-bg)', border: '1px solid var(--danger-border)', 'border-radius': '8px', color: 'var(--danger)', 'font-size': '12px', cursor: 'pointer', ...sansFont }}>
                        {translate('btn_delete')}
                      </button>
                    </div>
                    <Show when={field.field_type === 'multiselect'}>
                      <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '8px' }}>
                        <For each={field.options ?? []}>{choice => {
                          const picked = () => parseChoices(value()).includes(choice)
                          return (
                            <button onClick={() => {
                              const current = parseChoices(value())
                              setValue(serialiseChoices(picked()
                                ? current.filter(entry => entry !== choice)
                                : [...current, choice]))
                            }}
                              style={{ padding: '5px 12px', 'border-radius': '8px', border: `1px solid ${picked() ? 'var(--accent)' : 'var(--border2)'}`, background: picked() ? 'rgba(69,123,157,0.18)' : 'var(--surface)', color: picked() ? 'var(--accent-light)' : 'var(--muted)', 'font-size': '12px', cursor: 'pointer', ...sansFont, 'font-weight': picked() ? '600' : '400' }}>
                              {choice}
                            </button>
                          )
                        }}</For>
                      </div>
                    </Show>
                    <Show when={field.field_type !== 'multiselect'}>
                      <Show when={field.field_type === 'select'} fallback={
                        <Show when={field.field_type === 'boolean'} fallback={
                          <input style={inputStyle}
                            type={field.field_type === 'int' || field.field_type === 'float' ? 'number' : 'text'}
                            step={field.field_type === 'float' ? 'any' : '1'}
                            placeholder={translate(`custom_field_type_${field.field_type}` as any)}
                            value={value()} onInput={event => setValue(event.currentTarget.value)} />
                        }>
                          <div style={{ display: 'flex', 'align-items': 'center', gap: '10px' }}>
                            <ToggleSwitch checked={value() === '1'} onChange={on => setValue(on ? '1' : '0')} />
                            <span style={switchLabelStyle}>{translate(value() === '1' ? 'label_yes' : 'label_no')}</span>
                          </div>
                        </Show>
                      }>
                        <select style={inputStyle} value={value()} onChange={event => setValue(event.currentTarget.value)}>
                          <option value="">-</option>
                          <For each={field.options ?? []}>{choice => (
                            <option value={choice}>{choice}</option>
                          )}</For>
                        </select>
                      </Show>
                    </Show>
                  </div>
                )
              }}</For>

              <Show when={fieldsOnDesign().length === 0}>
                <div style={emptyStyle}>{translate('custom_fields_none_on_design')}</div>
              </Show>

              <Show when={fieldsOffDesign().length > 0}>
                <div style={{ 'margin-top': '16px' }}>
                  <select style={{ ...inputStyle, width: 'auto', 'min-width': '240px' }} value=""
                    onChange={event => {
                      const chosen = customFields().find(field => String(field.id) === event.currentTarget.value)
                      event.currentTarget.value = ''
                      if (!chosen) return
                      // A yes/no starts at no, which is a value like any other now that
                      // the field is deliberately on this design.
                      setCustomValues(current => ({
                        ...current,
                        [String(chosen.id)]: chosen.field_type === 'boolean' ? '0' : '',
                      }))
                    }}>
                    <option value="">＋ {translate('custom_field_add_to_design')}</option>
                    <For each={fieldsOffDesign()}>{field => (
                      <option value={String(field.id)}>{field.name}</option>
                    )}</For>
                  </select>
                </div>
              </Show>
            </div>
          </Show>

          <div style={{ ...hintStyle, 'margin-top': '0', padding: '0 4px' }}>
            {translate('edit_not_editable_hint')}
          </div>
        </div>
      </div>
    </div>
  )
}
