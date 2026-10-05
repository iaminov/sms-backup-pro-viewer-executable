import { useState, useEffect, useRef } from 'react'
import axios from 'axios'
import { Modal, Button, Form, Alert, ProgressBar, Badge } from 'react-bootstrap'

const API_BASE = import.meta.env.VITE_API_URL || '/api'

function MergeBackupsModal({ onClose }) {
  const [sourceFolder, setSourceFolder] = useState('')
  const [outputFile, setOutputFile] = useState('')
  const [includeMedia, setIncludeMedia] = useState(true)
  const [normalizeSchema, setNormalizeSchema] = useState(true)

  const [browsingFolder, setBrowsingFolder] = useState(false)
  const [browsingSave, setBrowsingSave] = useState(false)

  const [merging, setMerging] = useState(false)
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
        const res = await axios.get(`${API_BASE}/merge-backups/progress`)
        const data = res.data
        setProgress(data)

        if (data.status === 'completed') {
          clearInterval(pollIntervalRef.current)
          pollIntervalRef.current = null
          setMerging(false)
        } else if (data.status === 'error') {
          clearInterval(pollIntervalRef.current)
          pollIntervalRef.current = null
          setMerging(false)
          setError(data.error_message || 'Merge process failed.')
        }
      } catch (err) {
        console.error('Failed to poll merge progress:', err)
      }
    }, 400)
  }

  const handleBrowseFolder = async () => {
    try {
      setBrowsingFolder(true)
      const res = await axios.post(`${API_BASE}/merge-backups/browse-folder`)
      if (res.data?.path) {
        setSourceFolder(res.data.path)
        if (!outputFile) {
          setOutputFile(`${res.data.path}\\merged-sms-backup.xml`)
        }
      }
    } catch (err) {
      console.error('Failed to browse folder:', err)
    } finally {
      setBrowsingFolder(false)
    }
  }

  const handleBrowseSave = async () => {
    try {
      setBrowsingSave(true)
      const res = await axios.post(`${API_BASE}/merge-backups/browse-save-file`)
      if (res.data?.path) {
        setOutputFile(res.data.path)
      }
    } catch (err) {
      console.error('Failed to browse save destination:', err)
    } finally {
      setBrowsingSave(false)
    }
  }

  const handleStartMerge = async () => {
    setError(null)
    setOpenedFolder(false)

    if (!sourceFolder.trim()) {
      setError('Please select or specify the folder containing your XML and ZIP backups.')
      return
    }

    setMerging(true)
    setProgress({
      status: 'scanning',
      percent: 0,
      total_files: 0,
      processed_files: 0,
      total_found_messages: 0,
      unique_messages: 0,
      duplicates_removed: 0,
      current_file: ''
    })

    try {
      const payload = {
        source_folder: sourceFolder.trim(),
        output_file: outputFile.trim(),
        include_media: includeMedia,
        normalize_schema: normalizeSchema
      }
      await axios.post(`${API_BASE}/merge-backups`, payload)
      startPolling()
    } catch (err) {
      setMerging(false)
      const msg = err.response?.data?.error || err.message || 'Failed to start backup merge'
      setError(msg)
    }
  }

  const handleOpenOutput = async () => {
    try {
      await axios.post(`${API_BASE}/merge-backups/open-output`, {
        path: progress?.output_file || outputFile
      })
      setOpenedFolder(true)
      setTimeout(() => setOpenedFolder(false), 3000)
    } catch (err) {
      console.error('Failed to open output location:', err)
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
    <Modal show={true} onHide={merging ? null : onClose} size="lg" centered backdrop={merging ? 'static' : true}>
      <Modal.Header closeButton={!merging}>
        <Modal.Title className="d-flex align-items-center gap-2">
          <svg style={{ width: '1.4rem', height: '1.4rem' }} className="text-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 7v8a2 2 0 002 2h6M8 7V5a2 2 0 012-2h4.586a1 1 0 01.707.293l4.414 4.414a1 1 0 01.293.707V15a2 2 0 01-2 2h-2M8 7H6a2 2 0 00-2 2v10a2 2 0 002 2h8a2 2 0 002-2v-2" />
          </svg>
          Merge XML &amp; ZIP Backups
        </Modal.Title>
      </Modal.Header>
      <Modal.Body>
        <p className="text-muted small mb-3">
          Recursively scans any folder and all its subfolders for both extracted (<code>.xml</code>) and compressed (<code>.zip</code>) SMS Backup &amp; Restore files, eliminating all duplicate messages, normalizing schema changes, and sorting chronologically into one unified XML file.
        </p>

        {error && (
          <Alert variant="danger" dismissible={!merging} onClose={() => setError(null)}>
            {error}
          </Alert>
        )}

        {!merging && progress?.status === 'completed' && (
          <Alert variant="success" className="mb-4">
            <div className="d-flex justify-content-between align-items-center mb-2">
              <strong className="fs-6">Merge Successfully Completed!</strong>
              <Badge bg="success">{progress.duration}</Badge>
            </div>
            <p className="mb-2 small">
              Merged XML file created at: <br />
              <code className="text-break">{progress.output_file}</code>
            </p>
            <div className="d-flex flex-wrap gap-2 mb-3">
              <Badge bg="primary" className="p-2">📁 {progress.processed_files} Backup Files Processed</Badge>
              <Badge bg="info" className="p-2">🔍 {progress.total_found_messages.toLocaleString()} Total Scanned</Badge>
              <Badge bg="warning" text="dark" className="p-2">✨ {progress.unique_messages.toLocaleString()} Unique Messages</Badge>
              <Badge bg="danger" className="p-2">🗑️ {progress.duplicates_removed.toLocaleString()} Duplicates Removed</Badge>
              <Badge bg="secondary" className="p-2">💾 {formatBytes(progress.output_size)} Output Size</Badge>
            </div>
            <div className="d-flex gap-2 align-items-center">
              <Button variant="success" size="sm" onClick={handleOpenOutput} className="d-flex align-items-center gap-1">
                <span>📂</span> Show Merged File in Windows Explorer
              </Button>
              {openedFolder && <span className="text-success small fw-semibold">Opening in Explorer...</span>}
            </div>
          </Alert>
        )}

        {merging && (
          <div className="p-3 mb-4 rounded border bg-light shadow-sm">
            <div className="d-flex justify-content-between align-items-center mb-2">
              <span className="fw-bold text-primary">
                {progress?.status === 'scanning' && 'Scanning Folder & Subfolders...'}
                {progress?.status === 'merging' && `Reading & Deduplicating (${progress?.percent || 0}%)`}
                {progress?.status === 'writing' && `Writing Chronologically to XML (${progress?.percent || 0}%)`}
              </span>
              <span className="badge bg-secondary">
                Files: {progress?.processed_files || 0} / {progress?.total_files || 0}
              </span>
            </div>
            <ProgressBar
              animated={true}
              now={progress?.percent || 0}
              label={`${progress?.percent || 0}%`}
              variant="success"
              style={{ height: '22px', fontSize: '0.85rem', fontWeight: 'bold' }}
              className="mb-3"
            />
            {progress?.current_file && (
              <div className="small text-muted mb-2 text-truncate">
                <strong>Current:</strong> {progress.current_file}
              </div>
            )}
            <div className="d-flex flex-wrap gap-2 small">
              <Badge bg="info">Total Scanned: {(progress?.total_found_messages || 0).toLocaleString()}</Badge>
              <Badge bg="success">Unique Staged: {(progress?.unique_messages || progress?.total_found_messages || 0).toLocaleString()}</Badge>
              <Badge bg="warning" text="dark">Duplicates Removed: {(progress?.duplicates_removed || 0).toLocaleString()}</Badge>
            </div>
          </div>
        )}

        <Form>
          {/* Source Folder */}
          <Form.Group className="mb-3">
            <Form.Label className="fw-semibold">Source Directory</Form.Label>
            <div className="input-group">
              <Form.Control
                type="text"
                placeholder="e.g. C:\Users\YourName\Backups"
                value={sourceFolder}
                onChange={(e) => {
                  setSourceFolder(e.target.value)
                  if (!outputFile) {
                    setOutputFile(`${e.target.value}\\merged-sms-backup.xml`)
                  }
                }}
                disabled={merging}
              />
              <Button
                variant="outline-primary"
                onClick={handleBrowseFolder}
                disabled={merging || browsingFolder}
              >
                {browsingFolder ? 'Selecting...' : '📁 Browse Folder...'}
              </Button>
            </div>
            <Form.Text className="text-muted">
              Recursively finds all <code>.xml</code> and <code>.zip</code> backups inside this directory and all nested subfolders.
            </Form.Text>
          </Form.Group>

          {/* Output Destination */}
          <Form.Group className="mb-3">
            <Form.Label className="fw-semibold">Output XML File Destination</Form.Label>
            <div className="input-group">
              <Form.Control
                type="text"
                placeholder="e.g. C:\Users\YourName\Backups\merged-sms-backup.xml"
                value={outputFile}
                onChange={(e) => setOutputFile(e.target.value)}
                disabled={merging}
              />
              <Button
                variant="outline-secondary"
                onClick={handleBrowseSave}
                disabled={merging || browsingSave}
              >
                {browsingSave ? 'Selecting...' : '💾 Browse Destination...'}
              </Button>
            </div>
            <Form.Text className="text-muted">
              The single, unified XML file created after merging and deduplicating.
            </Form.Text>
          </Form.Group>

          {/* Options */}
          <div className="p-3 bg-light rounded border mb-3">
            <Form.Label className="fw-semibold mb-2">Merge Configuration</Form.Label>

            <Form.Check
              type="switch"
              id="include-media-toggle"
              label="Include Media (Photos, Videos, Audio) in Merged XML"
              checked={includeMedia}
              onChange={(e) => setIncludeMedia(e.target.checked)}
              disabled={merging}
              className="mb-1"
            />
            <p className="text-muted small ms-4 mb-3">
              {includeMedia
                ? 'Preserves all original embedded base64 media within MMS parts.'
                : 'Strips heavy base64 data attributes to produce a lightweight, fast-loading text-only XML file while preserving all metadata.'}
            </p>

            <Form.Check
              type="switch"
              id="normalize-schema-toggle"
              label="Normalize Schema Across Android &amp; App Versions"
              checked={normalizeSchema}
              onChange={(e) => setNormalizeSchema(e.target.checked)}
              disabled={merging}
              className="mb-1"
            />
            <p className="text-muted small ms-4 mb-0">
              Samples the newest backup file schema to guarantee consistent XML attribute ordering and standard defaults for older backups.
            </p>
          </div>

          <div className="p-2 border rounded bg-body-tertiary small text-muted">
            <span className="fw-semibold">💡 Performance &amp; Memory Protection:</span> Uses a disk-backed streaming pipeline and indexed deduplication keys. Can merge 20+ GB backup libraries with constant minimal RAM usage (&lt; 50 MB).
          </div>
        </Form>
      </Modal.Body>
      <Modal.Footer>
        <Button variant="secondary" onClick={onClose} disabled={merging}>
          Close
        </Button>
        <Button
          variant="primary"
          onClick={handleStartMerge}
          disabled={merging || !sourceFolder.trim()}
          className="d-flex align-items-center gap-1"
        >
          {merging ? (
            <>
              <span className="spinner-border spinner-border-sm" role="status" aria-hidden="true"></span>
              Merging Backups...
            </>
          ) : (
            <>
              <span>⚡</span> Start Merge &amp; Deduplication
            </>
          )}
        </Button>
      </Modal.Footer>
    </Modal>
  )
}

export default MergeBackupsModal
