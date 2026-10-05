import { useState, useEffect, useRef } from 'react'
import axios from 'axios'
import { Modal, Button, Form, Alert, ProgressBar, Badge } from 'react-bootstrap'

const API_BASE = import.meta.env.VITE_API_URL || '/api'

function MergeBackupsModal({ onClose }) {
  const [sourceFolder, setSourceFolder] = useState('')
  const [outputFile, setOutputFile] = useState('')
  const [includeMedia, setIncludeMedia] = useState(true)
  const [normalizeSchema, setNormalizeSchema] = useState(true)
  const [normalizeMyNumber, setNormalizeMyNumber] = useState(true)
  const [detectedNumbers, setDetectedNumbers] = useState([])
  const [selectedPrimaryNumber, setSelectedPrimaryNumber] = useState('')
  const [customPrimaryNumber, setCustomPrimaryNumber] = useState('')
  const [detectingNumbers, setDetectingNumbers] = useState(false)
  const [detectionMessage, setDetectionMessage] = useState('')
  const [signalPassphrase, setSignalPassphrase] = useState('')
  const [showSignalPassphrase, setShowSignalPassphrase] = useState(false)

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

  const handleDetectNumbers = async (folder, passphrase) => {
    const f = folder || sourceFolder
    if (!f || !f.trim()) return
    try {
      setDetectingNumbers(true)
      setDetectionMessage('')
      const res = await axios.post(`${API_BASE}/merge-backups/detect-numbers`, {
        source_folder: f.trim(),
        signal_passphrase: (passphrase !== undefined ? passphrase : signalPassphrase).trim()
      })
      const list = res.data?.numbers || []
      setDetectedNumbers(list)
      if (list.length > 0) {
        const first = list[0].phone || list[0].number
        if (!selectedPrimaryNumber || selectedPrimaryNumber === '__custom__') {
          setSelectedPrimaryNumber(res.data.primary_candidate || first)
        }
      } else {
        setDetectionMessage('No phone numbers automatically detected. You can enter your primary number manually below.')
      }
    } catch (err) {
      console.warn('Failed to detect candidate numbers:', err)
    } finally {
      setDetectingNumbers(false)
    }
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
        handleDetectNumbers(res.data.path, signalPassphrase)
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
      setError('Please select or specify the folder containing your XML, ZIP, Signal, or Google Voice backups.')
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
      const targetNumber = selectedPrimaryNumber === '__custom__'
        ? customPrimaryNumber.trim()
        : (selectedPrimaryNumber || customPrimaryNumber.trim())

      const alternateNumbers = detectedNumbers
        .map(n => n.phone || n.number)
        .filter(num => num && num !== targetNumber)

      const payload = {
        source_folder: sourceFolder.trim(),
        output_file: outputFile.trim(),
        include_media: includeMedia,
        normalize_schema: normalizeSchema,
        signal_passphrase: signalPassphrase.trim(),
        normalize_my_number: normalizeMyNumber,
        target_my_number: targetNumber,
        alternate_my_numbers: alternateNumbers
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
          Recursively scans any folder and all its subfolders for standard Android (<code>.xml</code>), compressed (<code>.zip</code>), encrypted Signal (<code>.backup</code>), and Google Voice Takeout exports (HTML / <code>.zip</code>), eliminating duplicate messages &amp; calls, normalizing schemas, and sorting chronologically into one unified XML file.
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
              <Badge bg="warning" text="dark" className="p-2">✨ {progress.unique_messages.toLocaleString()} Unique Records</Badge>
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
          <div className="p-3 mb-4 rounded border bg-body-tertiary shadow-sm">
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
              Recursively finds all <code>.xml</code>, <code>.zip</code>, Signal <code>.backup</code>, and Google Voice Takeout files inside this directory and all nested subfolders.
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

          {/* Signal Backup Passphrase */}
          <Form.Group className="mb-3">
            <Form.Label className="fw-semibold d-flex justify-content-between align-items-center">
              <span>Signal Backup Passphrase <span className="text-muted fw-normal small">(Optional, for <code>.backup</code> files)</span></span>
              <Button
                variant="link"
                size="sm"
                className="p-0 text-decoration-none"
                onClick={() => setShowSignalPassphrase(!showSignalPassphrase)}
              >
                {showSignalPassphrase ? 'Hide' : 'Show'}
              </Button>
            </Form.Label>
            <Form.Control
              type={showSignalPassphrase ? 'text' : 'password'}
              placeholder="e.g. 31889 30544 62782 17192 51469 48815"
              value={signalPassphrase}
              onChange={(e) => setSignalPassphrase(e.target.value)}
              disabled={merging}
              autoComplete="off"
            />
            <Form.Text className="text-muted">
              Enter the 30-digit passphrase used when creating your Signal Android backup. If present in your selected directory, Signal backups will be decrypted, converted to standard SMS/MMS, and merged chronologically with duplicates removed.
            </Form.Text>
          </Form.Group>

          {/* Options */}
          <div className="p-3 bg-body-tertiary rounded border mb-3">
            <Form.Label className="fw-semibold mb-2">Merge Configuration</Form.Label>

            {/* Normalize My Phone Number */}
            <Form.Check
              type="switch"
              id="normalize-number-toggle"
              label="Normalize 'My' Phone Number (Handles Phone Number Changes & Merges)"
              checked={normalizeMyNumber}
              onChange={(e) => setNormalizeMyNumber(e.target.checked)}
              disabled={merging}
              className="mb-1"
            />
            <p className="text-muted small ms-4 mb-2">
              Essential if you changed phone numbers, switched SIM cards, or have backups from multiple services (Signal, Google Voice, SMS). Standardizes participant lists and merges conversations seamlessly.
            </p>

            {normalizeMyNumber && (
              <div className="ms-4 p-3 bg-white border rounded mb-3">
                <div className="d-flex justify-content-between align-items-center mb-2">
                  <Form.Label className="fw-semibold mb-0 small">Primary Phone Number ("Me")</Form.Label>
                  <Button
                    variant="outline-secondary"
                    size="sm"
                    onClick={() => handleDetectNumbers(sourceFolder, signalPassphrase)}
                    disabled={merging || detectingNumbers || !sourceFolder.trim()}
                    className="py-0 px-2"
                    style={{ fontSize: '0.75rem' }}
                  >
                    {detectingNumbers ? (
                      <>
                        <span className="spinner-border spinner-border-sm me-1" role="status" aria-hidden="true" style={{ width: '0.7rem', height: '0.7rem' }}></span>
                        Detecting...
                      </>
                    ) : (
                      '🔍 Detect Numbers'
                    )}
                  </Button>
                </div>

                {detectedNumbers.length > 0 ? (
                  <Form.Select
                    size="sm"
                    value={selectedPrimaryNumber}
                    onChange={(e) => setSelectedPrimaryNumber(e.target.value)}
                    disabled={merging || detectingNumbers}
                    className="mb-2"
                  >
                    {detectedNumbers.map((item) => {
                      const num = item.phone || item.number
                      const disp = item.formatted || item.display || num
                      const src = item.source || (item.sources ? item.sources.join(', ') : '')
                      return (
                        <option key={num} value={num}>
                          {disp} ({item.count ? item.count.toLocaleString() : 0} msgs{src ? ` · ${src}` : ''})
                        </option>
                      )
                    })}
                    <option value="__custom__">Custom / Enter Manually...</option>
                  </Form.Select>
                ) : (
                  <div className="text-muted small mb-2 fst-italic">
                    {detectingNumbers ? 'Scanning backups for phone numbers...' : (detectionMessage || 'Select a folder or click "Detect Numbers" to scan candidate phone numbers.')}
                  </div>
                )}

                {(selectedPrimaryNumber === '__custom__' || detectedNumbers.length === 0) && (
                  <Form.Control
                    type="text"
                    size="sm"
                    placeholder="Enter your phone number (e.g. +1 555-123-4567)"
                    value={customPrimaryNumber}
                    onChange={(e) => setCustomPrimaryNumber(e.target.value)}
                    disabled={merging}
                    className="mt-1"
                  />
                )}

                {detectedNumbers.length > 1 && selectedPrimaryNumber !== '__custom__' && (
                  <div className="small text-muted mt-1">
                    ℹ️ Other detected numbers ({detectedNumbers.filter(n => (n.phone || n.number) !== selectedPrimaryNumber).map(n => n.formatted || n.display || n.phone || n.number).join(', ')}) will be recognized as your former numbers and normalized to your primary identity.
                  </div>
                )}
              </div>
            )}

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
