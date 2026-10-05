import { useState, useEffect, useRef } from 'react'
import axios from 'axios'
import { Modal, Button, Form, Alert, ProgressBar, Badge, Tab, Tabs, InputGroup } from 'react-bootstrap'

const API_BASE = import.meta.env.VITE_API_URL || '/api'

function ExtractMediaModal({ onClose }) {
  const [sourceType, setSourceType] = useState('local') // 'local' or 'upload'
  const [filePath, setFilePath] = useState('')
  const [selectedFile, setSelectedFile] = useState(null)
  const [outputDir, setOutputDir] = useState('./media')
  const [extractImg, setExtractImg] = useState(true)
  const [extractVid, setExtractVid] = useState(true)
  const [extractAud, setExtractAud] = useState(true)
  const [convertHeic, setConvertHeic] = useState(true)
  const [groupByConversation, setGroupByConversation] = useState(true)

  const [browsingFolder, setBrowsingFolder] = useState(false)
  const [browsingFile, setBrowsingFile] = useState(false)

  const [extracting, setExtracting] = useState(false)
  const [progress, setProgress] = useState(null)
  const [error, setError] = useState(null)
  const [openedFolder, setOpenedFolder] = useState(false)

  const pollIntervalRef = useRef(null)

  useEffect(() => {
    return () => {
      if (pollIntervalRef.current) {
        clearInterval(pollIntervalRef.current)
      }
    }
  }, [])

  const startPolling = () => {
    if (pollIntervalRef.current) {
      clearInterval(pollIntervalRef.current)
    }

    pollIntervalRef.current = setInterval(async () => {
      try {
        const res = await axios.get(`${API_BASE}/extract-media/progress`)
        const data = res.data
        setProgress(data)

        if (data.status === 'completed') {
          clearInterval(pollIntervalRef.current)
          pollIntervalRef.current = null
          setExtracting(false)
        } else if (data.status === 'error') {
          clearInterval(pollIntervalRef.current)
          pollIntervalRef.current = null
          setExtracting(false)
          setError(data.error_message || 'Media extraction encountered an error.')
        }
      } catch (err) {
        console.error('Failed to poll extraction progress:', err)
      }
    }, 400)
  }

  const handleBrowseFolder = async () => {
    try {
      setBrowsingFolder(true)
      const res = await axios.post(`${API_BASE}/extract-media/browse-folder`)
      if (res.data?.path) {
        setOutputDir(res.data.path)
      }
    } catch (err) {
      console.error('Failed to browse folder:', err)
    } finally {
      setBrowsingFolder(false)
    }
  }

  const handleBrowseFile = async () => {
    try {
      setBrowsingFile(true)
      const res = await axios.post(`${API_BASE}/extract-media/browse-file`)
      if (res.data?.path) {
        setFilePath(res.data.path)
      }
    } catch (err) {
      console.error('Failed to browse XML file:', err)
    } finally {
      setBrowsingFile(false)
    }
  }

  const handleStartExtraction = async () => {
    setError(null)
    setOpenedFolder(false)

    if (sourceType === 'local') {
      if (!filePath.trim()) {
        setError('Please select or enter the path to your SMS Backup XML or ZIP file.')
        return
      }
    } else {
      if (!selectedFile) {
        setError('Please select an XML or ZIP backup file to extract.')
        return
      }
    }

    setExtracting(true)
    setProgress({
      status: 'extracting',
      percent: 0,
      processed_mms: 0,
      images_extracted: 0,
      videos_extracted: 0,
      audio_extracted: 0,
      other_extracted: 0,
      extracted_bytes: 0
    })

    try {
      if (sourceType === 'local') {
        const payload = {
          file_path: filePath.trim(),
          output_dir: outputDir.trim(),
          convert_heic: convertHeic,
          extract_images: extractImg,
          extract_videos: extractVid,
          extract_audio: extractAud,
          group_by_conversation: groupByConversation
        }
        await axios.post(`${API_BASE}/extract-media`, payload)
      } else {
        const formData = new FormData()
        formData.append('file', selectedFile)
        formData.append('output_dir', outputDir.trim())
        formData.append('convert_heic', convertHeic ? 'true' : 'false')
        formData.append('extract_images', extractImg ? 'true' : 'false')
        formData.append('extract_videos', extractVid ? 'true' : 'false')
        formData.append('extract_audio', extractAud ? 'true' : 'false')
        formData.append('group_by_conversation', groupByConversation ? 'true' : 'false')

        await axios.post(`${API_BASE}/extract-media`, formData, {
          headers: { 'Content-Type': 'multipart/form-data' }
        })
      }

      startPolling()
    } catch (err) {
      setExtracting(false)
      const msg = err.response?.data?.error || err.message || 'Failed to start media extraction'
      setError(msg)
    }
  }

  const handleOpenFolder = async () => {
    try {
      await axios.post(`${API_BASE}/extract-media/open-folder`, {
        path: progress?.output_dir || outputDir
      })
      setOpenedFolder(true)
      setTimeout(() => setOpenedFolder(false), 3000)
    } catch (err) {
      console.error('Failed to open folder:', err)
    }
  }

  const formatBytes = (bytes) => {
    if (!bytes || bytes === 0) return '0 B'
    const k = 1024
    const sizes = ['B', 'KB', 'MB', 'GB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
  }

  return (
    <Modal show={true} onHide={extracting ? null : onClose} size="lg" centered backdrop={extracting ? 'static' : true}>
      <Modal.Header closeButton={!extracting}>
        <Modal.Title className="d-flex align-items-center gap-2">
          <svg style={{ width: '1.4rem', height: '1.4rem' }} className="text-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
          </svg>
          Extract Media Files from XML / ZIP
        </Modal.Title>
      </Modal.Header>
      <Modal.Body>
        <p className="text-muted small mb-3">
          Extract and decode all photos, videos, and audio attachments embedded inside your SMS/MMS XML or ZIP (.xml, .zip) backup file into organized folders on your computer.
        </p>

        {error && (
          <Alert variant="danger" dismissible={!extracting} onClose={() => setError(null)}>
            {error}
          </Alert>
        )}

        {!extracting && progress?.status === 'completed' && (
          <Alert variant="success" className="mb-4">
            <div className="d-flex justify-content-between align-items-center mb-2">
              <strong className="fs-6">Extraction Complete!</strong>
              <Badge bg="success">{progress.duration}</Badge>
            </div>
            <p className="mb-2 small">
              All media files have been extracted and organized in: <code>{progress.output_dir}</code>
            </p>
            <div className="d-flex flex-wrap gap-2 mb-3">
              <Badge bg="primary" className="p-2">🖼️ {progress.images_extracted} Images</Badge>
              <Badge bg="info" className="p-2">🎥 {progress.videos_extracted} Videos</Badge>
              <Badge bg="secondary" className="p-2">🎵 {progress.audio_extracted} Audio Notes</Badge>
              {progress.other_extracted > 0 && (
                <Badge bg="dark" className="p-2">📎 {progress.other_extracted} Other</Badge>
              )}
              <Badge bg="secondary" className="p-2">💾 {formatBytes(progress.extracted_bytes || progress.total_bytes)} Total</Badge>
            </div>
            <div className="d-flex gap-2 align-items-center">
              <Button variant="success" size="sm" onClick={handleOpenFolder} className="d-flex align-items-center gap-1">
                <span>📁</span> Open Media Folder in Windows Explorer
              </Button>
              {openedFolder && <span className="text-success small fw-semibold">Opening folder...</span>}
            </div>
          </Alert>
        )}

        {extracting && (
          <div className="p-3 mb-4 rounded border bg-body-tertiary shadow-sm">
            <div className="d-flex justify-content-between align-items-center mb-2">
              <span className="fw-bold text-primary">
                Extracting Media Files ({progress?.percent || 0}%)
              </span>
              <span className="badge bg-secondary">MMS Processed: {progress?.processed_mms || 0}</span>
            </div>
            <ProgressBar
              animated={true}
              now={progress?.percent || 0}
              label={`${progress?.percent || 0}%`}
              variant="success"
              style={{ height: '22px', fontSize: '0.85rem', fontWeight: 'bold' }}
              className="mb-3"
            />
            <div className="d-flex flex-wrap gap-2 small">
              <Badge bg="primary">🖼️ Images: {progress?.images_extracted || 0}</Badge>
              <Badge bg="info">🎥 Videos: {progress?.videos_extracted || 0}</Badge>
              <Badge bg="secondary">🎵 Audio: {progress?.audio_extracted || 0}</Badge>
              {progress?.other_extracted > 0 && (
                <Badge bg="dark">📎 Other: {progress?.other_extracted || 0}</Badge>
              )}
              <Badge bg="secondary">💾 {formatBytes(progress?.extracted_bytes || 0)} Extracted</Badge>
            </div>
          </div>
        )}

        <Form>
          <Tabs
            activeKey={sourceType}
            onSelect={(k) => setSourceType(k)}
            className="mb-3"
          >
            <Tab eventKey="local" title="Local XML or ZIP File (Recommended - Fastest)">
              <Form.Group className="mb-3">
                <Form.Label className="fw-semibold small">Select SMS Backup File (.xml or .zip)</Form.Label>
                <InputGroup>
                  <Form.Control
                    type="text"
                    placeholder="Click Browse or enter path to XML or ZIP file..."
                    value={filePath}
                    onChange={(e) => setFilePath(e.target.value)}
                    disabled={extracting}
                  />
                  <Button
                    variant="outline-primary"
                    onClick={handleBrowseFile}
                    disabled={extracting || browsingFile}
                    className="d-flex align-items-center gap-1"
                  >
                    <span>📄</span>
                    {browsingFile ? 'Opening...' : 'Browse File (.xml, .zip)...'}
                  </Button>
                </InputGroup>
                <Form.Text className="text-muted">
                  Reads directly from disk with zero memory overhead or browser upload waiting time.
                </Form.Text>
              </Form.Group>
            </Tab>
            <Tab eventKey="upload" title="Upload XML or ZIP from Browser">
              <Form.Group className="mb-3">
                <Form.Label className="fw-semibold small">Choose XML or ZIP Backup</Form.Label>
                <Form.Control
                  type="file"
                  accept=".xml,.zip"
                  onChange={(e) => setSelectedFile(e.target.files[0] || null)}
                  disabled={extracting}
                />
                <Form.Text className="text-muted">
                  Select an XML or ZIP (.zip, .xml.zip) backup from your computer.
                </Form.Text>
              </Form.Group>
            </Tab>
          </Tabs>

          <Form.Group className="mb-3">
            <Form.Label className="fw-semibold small">Destination Directory</Form.Label>
            <InputGroup>
              <Form.Control
                type="text"
                value={outputDir}
                onChange={(e) => setOutputDir(e.target.value)}
                disabled={extracting}
              />
              <Button
                variant="outline-secondary"
                onClick={handleBrowseFolder}
                disabled={extracting || browsingFolder}
                className="d-flex align-items-center gap-1"
              >
                <span>📁</span>
                {browsingFolder ? 'Opening...' : 'Browse Folder...'}
              </Button>
            </InputGroup>
            <Form.Text className="text-muted">
              Select or type the destination folder where extracted media will be stored.
            </Form.Text>
          </Form.Group>

          <div className="mb-3 p-3 bg-body-tertiary rounded border">
            <Form.Check
              type="checkbox"
              id="group-by-conversation"
              label="Create separate subfolders for each conversation"
              checked={groupByConversation}
              onChange={(e) => setGroupByConversation(e.target.checked)}
              disabled={extracting}
              className="fw-semibold"
            />
            <div className="text-muted small ps-4 mt-1">
              {groupByConversation ? (
                <span>Files will be organized by contact/group name: <code>Destination/&lt;ContactName&gt;/image/</code>, <code>video/</code>, <code>audio/</code></span>
              ) : (
                <span>All files will be placed into central media type folders: <code>Destination/image/</code>, <code>video/</code>, <code>audio/</code></span>
              )}
            </div>
          </div>

          <div className="mb-3 p-3 bg-body-tertiary rounded border">
            <Form.Label className="fw-semibold small d-block mb-2">Media Types to Extract</Form.Label>
            <div className="d-flex flex-wrap gap-3">
              <Form.Check
                type="checkbox"
                id="extract-img"
                label="Photos & Images (JPEG, PNG, HEIC, GIF)"
                checked={extractImg}
                onChange={(e) => setExtractImg(e.target.checked)}
                disabled={extracting}
              />
              <Form.Check
                type="checkbox"
                id="extract-vid"
                label="Videos (MP4, 3GP, MOV)"
                checked={extractVid}
                onChange={(e) => setExtractVid(e.target.checked)}
                disabled={extracting}
              />
              <Form.Check
                type="checkbox"
                id="extract-aud"
                label="Audio Notes (AMR, MP3, M4A)"
                checked={extractAud}
                onChange={(e) => setExtractAud(e.target.checked)}
                disabled={extracting}
              />
              <Form.Check
                type="checkbox"
                id="convert-heic"
                label="Convert Apple HEIC images to JPEG"
                checked={convertHeic}
                onChange={(e) => setConvertHeic(e.target.checked)}
                disabled={extracting}
              />
            </div>
          </div>
        </Form>
      </Modal.Body>
      <Modal.Header as="div" className="modal-footer">
        <Button variant="secondary" onClick={onClose} disabled={extracting}>
          {progress?.status === 'completed' ? 'Close' : 'Cancel'}
        </Button>
        <Button
          variant="primary"
          onClick={handleStartExtraction}
          disabled={extracting || (!extractImg && !extractVid && !extractAud)}
          className="d-flex align-items-center gap-2"
        >
          {extracting ? (
            <>
              <span className="spinner-border spinner-border-sm" role="status" aria-hidden="true"></span>
              Extracting...
            </>
          ) : (
            <>
              <svg style={{ width: '1rem', height: '1rem' }} fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
              </svg>
              Start Extraction
            </>
          )}
        </Button>
      </Modal.Header>
    </Modal>
  )
}

export default ExtractMediaModal
