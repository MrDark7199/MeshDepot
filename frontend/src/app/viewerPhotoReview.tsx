import { Show } from 'solid-js'
import type { Setter } from 'solid-js'
import type { StlViewerModalProps } from '../components/StlViewer'

export interface PhotoReviewDeps {
  props: StlViewerModalProps
  translate: (key: any, params?: any) => string
  photoMode: () => 'off' | 'aim' | 'review'
  setPhotoMode: Setter<'off' | 'aim' | 'review'>
  photoUrl: () => string | null
  photoSaving: () => boolean
  photoSaved: () => boolean
  photoError: () => boolean
  downloadPhoto: () => void
  addPhotoToDesign: () => void
  exitPhoto: () => void
}

/** The photo tool's review step: preview, download, add to the gallery. */
export function photoReview(deps: PhotoReviewDeps) {
  const { props, translate, photoMode, setPhotoMode, photoUrl, photoSaving, photoSaved, photoError,
    downloadPhoto, addPhotoToDesign, exitPhoto } = deps
  return (
    <>
        {/* Photo tool - review: preview the capture, then download and/or add to the design gallery. */}
        <Show when={photoMode() === 'review' && photoUrl()}>
          <div style={{ position: 'absolute', inset: '0', background: 'rgba(0,0,0,0.6)', 'backdrop-filter': 'blur(4px)', display: 'flex', 'align-items': 'center', 'justify-content': 'center', 'z-index': '10', padding: '24px' }}>
            <div style={{ background: 'rgba(13,17,23,0.96)', border: '1px solid rgba(255,255,255,0.14)', 'border-radius': '14px', padding: '16px', display: 'flex', 'flex-direction': 'column', gap: '14px', 'max-width': 'min(88vw, 720px)', 'max-height': '88vh', 'box-shadow': '0 12px 40px rgba(0,0,0,0.55)' }}>
              <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '13px', 'font-weight': '600', color: 'rgba(255,255,255,0.9)' }}>
                {translate('viewer_photo_title')}
              </span>
              <img src={photoUrl()!} alt="" style={{ 'max-width': '100%', 'max-height': '60vh', 'object-fit': 'contain', 'border-radius': '8px', background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.08)' }} />
              <Show when={photoError()}>
                <span style={{ 'font-family': "'DM Sans',sans-serif", 'font-size': '12px', color: 'var(--danger)' }}>
                  {translate('viewer_photo_failed')}
                </span>
              </Show>
              <div style={{ display: 'flex', 'flex-wrap': 'wrap', gap: '8px', 'justify-content': 'flex-end' }}>
                <button onClick={() => setPhotoMode('aim')}
                  style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '8px 14px', color: 'rgba(255,255,255,0.85)', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif", 'margin-right': 'auto' }}>
                  ↺ {translate('viewer_photo_retake')}
                </button>
                <button onClick={downloadPhoto}
                  style={{ background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.18)', 'border-radius': '8px', padding: '8px 14px', color: '#fff', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                  ⭳ {translate('viewer_photo_download')}
                </button>
                <Show when={props.onSaveImage}>
                  <button onClick={addPhotoToDesign} disabled={photoSaving() || photoSaved()}
                    style={{ background: photoSaved() ? 'rgba(46,160,67,0.9)' : 'var(--accent)', border: `1px solid ${photoSaved() ? 'rgba(46,160,67,1)' : 'var(--accent)'}`, 'border-radius': '8px', padding: '8px 14px', color: '#fff', 'font-size': '12px', cursor: photoSaving() || photoSaved() ? 'default' : 'pointer', 'font-family': "'DM Sans',sans-serif", opacity: photoSaving() ? '0.7' : '1' }}>
                    {photoSaved() ? `✓ ${translate('viewer_photo_added')}` : photoSaving() ? translate('viewer_photo_adding') : `＋ ${translate('viewer_photo_add')}`}
                  </button>
                </Show>
                <button onClick={exitPhoto}
                  style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.15)', 'border-radius': '8px', padding: '8px 14px', color: 'rgba(255,255,255,0.85)', 'font-size': '12px', cursor: 'pointer', 'font-family': "'DM Sans',sans-serif" }}>
                  {translate('viewer_photo_done')}
                </button>
              </div>
            </div>
          </div>
        </Show>

    </>
  )
}
