import React, { useState } from 'react'
import {
  CheckCircle,
  XCircle,
  Copy,
  FileText,
  Code,
  AlertCircle,
  Download
} from 'lucide-react'
import { formatDuration } from '../../utils/formatDuration'
import type { ToolCallEndEvent, ToolCallErrorEvent } from '../../generated/events'

// Enhanced content type detection
interface ParsedContent {
  type: 'json-text' | 'structured-data' | 'plain-text' | 'error' | 'code' | 'unknown' | 'image-gen-result'
  content: string
  metadata?: Record<string, unknown>
}

const parseContent = (content: string): ParsedContent => {
  try {
    const parsed = JSON.parse(content)
    
    // Handle {"type":"text","text":"..."} format
    if (parsed.type === 'text' && parsed.text) {
      return {
        type: 'json-text',
        content: parsed.text.replace(/\\n/g, '\n'),
        metadata: { originalType: 'json-text' }
      }
    }
    
    // Handle image generation result (workspace-saved or base64 fallback)
    if (
      typeof parsed === 'object' &&
      typeof parsed.count === 'number' &&
      (Array.isArray(parsed.saved_paths) || Array.isArray(parsed.images))
    ) {
      return {
        type: 'image-gen-result',
        content: content,
        metadata: { originalData: parsed }
      }
    }

    // Handle structured data (PRs, file changes, etc.)
    if (typeof parsed === 'object' && !parsed.type) {
      return {
        type: 'structured-data',
        content: JSON.stringify(parsed, null, 2),
        metadata: { originalData: parsed }
      }
    }
    
    return {
      type: 'json-text',
      content: content,
      metadata: { originalType: 'json' }
    }
  } catch {
    // Check if it's an error message
    if (content.toLowerCase().includes('error') || content.toLowerCase().includes('failed')) {
      return {
        type: 'error',
        content: content,
        metadata: { originalType: 'error' }
      }
    }
    
    // Check if it looks like code
    if (content.includes('```') || content.includes('function') || content.includes('const ')) {
      return {
        type: 'code',
        content: content,
        metadata: { originalType: 'code' }
      }
    }
    
    // Default to plain text
    return {
      type: 'plain-text',
      content: content.replace(/\\n/g, '\n'),
      metadata: { originalType: 'text' }
    }
  }
}

// Copy to clipboard utility
const copyToClipboard = async (text: string) => {
  try {
    await navigator.clipboard.writeText(text)
    // You could add a toast notification here
  } catch (err) {
    console.error('Failed to copy to clipboard:', err)
  }
}

// Response type badge component
const ResponseTypeBadge: React.FC<{ type: ParsedContent['type'] }> = ({ type }) => {
  const getBadgeConfig = (type: ParsedContent['type']) => {
    switch (type) {
      case 'json-text':
        return { label: 'JSON Text', color: 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200' }
      case 'structured-data':
        return { label: 'Structured', color: 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200' }
      case 'plain-text':
        return { label: 'Text', color: 'bg-muted text-foreground' }
      case 'error':
        return { label: 'Error', color: 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200' }
      case 'code':
        return { label: 'Code', color: 'bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200' }
      case 'image-gen-result':
        return { label: 'Images', color: 'bg-pink-100 text-pink-800 dark:bg-pink-900 dark:text-pink-200' }
      default:
        return { label: 'Unknown', color: 'bg-muted text-foreground' }
    }
  }
  
  const config = getBadgeConfig(type)
  
  return (
    <span className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${config.color}`}>
      {config.label}
    </span>
  )
}

// Structured data renderer for PRs, file changes, etc.
const StructuredDataRenderer: React.FC<{ content: string; metadata?: Record<string, unknown> }> = ({ content, metadata }) => {
  try {
    const data = metadata?.originalData || JSON.parse(content)
    
    // Handle GitHub PR data
    if (data.title && data.author && data.state) {
      return (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold text-foreground">
              Pull Request: {data.title}
            </h3>
          </div>
          
          <div className="grid grid-cols-2 gap-2 text-xs">
            <div><strong>Author:</strong> {data.author}</div>
            <div><strong>State:</strong> 
              <span className={`ml-1 px-2 py-1 rounded-full text-xs ${
                data.state === 'open' ? 'bg-green-100 text-green-800' : 'bg-muted text-foreground'
              }`}>
                {data.state}
              </span>
            </div>
            <div><strong>Created:</strong> {new Date(data.created_at).toLocaleDateString()}</div>
            <div><strong>Updated:</strong> {new Date(data.updated_at).toLocaleDateString()}</div>
          </div>
          
          <div className="mt-3 p-3 bg-muted/50 rounded-md max-h-96 overflow-y-auto">
            <pre className="text-xs overflow-x-auto">{JSON.stringify(data, null, 2)}</pre>
          </div>
        </div>
      )
    }
    
    // Handle file changes data
    if (content.includes('File changes for PR') || content.includes('Status: added')) {
      return (
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold text-foreground">
              File Changes
            </h3>
          </div>
          
          <div className="p-3 bg-muted/50 rounded-md max-h-96 overflow-y-auto">
            <pre className="text-xs whitespace-pre-wrap break-all overflow-x-auto">{content}</pre>
          </div>
        </div>
      )
    }
    
    // Default structured data display
    return (
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-foreground">
            Structured Data
          </h3>
        </div>
        
        <div className="p-3 bg-muted/50 rounded-md max-h-96 overflow-y-auto">
          <div className="text-xs whitespace-pre-wrap break-all overflow-x-auto">{content}</div>
        </div>
      </div>
    )
  } catch {
    return <div className="text-sm text-muted-foreground">{content}</div>
  }
}

// Enhanced tool response display component
export const EnhancedToolResponseDisplay: React.FC<{ 
  event: ToolCallEndEvent | ToolCallErrorEvent
  content: string
}> = ({ event, content }) => {
  const [copied, setCopied] = useState(false)
  
  // Add debug logging
  // EnhancedToolResponseDisplay received content
  // Content type and length
  
  const parsedContent = parseContent(content)
  // Parsed content
  
  const isError = 'error' in event
  
  // Copy to clipboard handler
  const handleCopy = async () => {
    await copyToClipboard(content)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }
  
  // Single-line format for all modes
  return (
    <div className={`border rounded-md p-3 ${
      isError 
        ? 'bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800' 
        : 'bg-green-50 dark:bg-green-900/20 border-green-200 dark:border-green-800'
    }`}>
      {/* Header with metadata */}
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2 text-sm">
          {isError ? (
            <XCircle className="w-4 h-4 text-red-600" />
          ) : (
            <CheckCircle className="w-4 h-4 text-green-600" />
          )}
          <span className="font-medium">
            {isError ? 'Tool Call Failed' : 'Tool Call Completed'}
          </span>
          <span className="text-xs text-muted-foreground">•</span>
          <span className="text-xs">Turn: {event.turn}</span>
          <span className="text-xs text-muted-foreground">•</span>
          <span className="text-xs">Tool: {event.tool_name}</span>
          <span className="text-xs text-muted-foreground">•</span>
          <span className="text-xs">Server: {event.server_name}</span>
          {!isError && event.duration && (
            <>
              <span className="text-xs text-muted-foreground">•</span>
              <span className="text-xs">Duration: {formatDuration(event.duration)}</span>
            </>
          )}
        </div>
        
        <div className="flex items-center gap-2">
          <ResponseTypeBadge type={parsedContent.type} />
          <button
            onClick={handleCopy}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
          >
            <Copy className="w-3 h-3" />
            {copied ? 'Copied!' : 'Copy'}
          </button>
        </div>
      </div>
      
      {/* Content with scrollbar - always visible */}
      <div className="mt-2">
        <div className="text-xs font-medium mb-2 text-foreground">
          {isError ? 'Error Details:' : 'Response:'}
        </div>
        
        <div className="border border-border rounded-md p-3 bg-card max-h-96 overflow-y-auto overflow-x-auto">
          <EnhancedContentRenderer 
            parsedContent={parsedContent} 
          />
        </div>
      </div>
    </div>
  )
}

// Enhanced content renderer component
const EnhancedContentRenderer: React.FC<{ 
  parsedContent: ParsedContent
}> = ({ parsedContent }) => {
  switch (parsedContent.type) {
    case 'json-text':
      return (
        <div className="text-sm text-foreground">
          <div className="flex items-center gap-2 mb-2">
            <FileText className="w-3 h-3" />
            <span className="font-medium">JSON Text Response</span>
          </div>
          <div className="whitespace-pre-wrap break-words leading-relaxed">
            {parsedContent.content.split('\n').map((line, index) => (
              <div key={index} className="mb-1">
                {line || '\u00A0'}
              </div>
            ))}
          </div>
        </div>
      )
      
    case 'image-gen-result': {
      const data = parsedContent.metadata?.originalData as {
        images?: Array<{ data: string; mime_type: string }>
        model: string
        cost_per_image?: number | null
        cost_note?: string
        prompt: string
        saved_paths?: string[]
        count: number
      }
      const imageCost =
        typeof data.cost_per_image === 'number'
          ? `$${data.cost_per_image.toFixed(2)}/image`
          : data.cost_note || 'Cost unavailable'
      return (
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            Generated {data.count} image{data.count !== 1 ? 's' : ''} · {data.model} · {imageCost}
          </p>
          {data.images && data.images.length > 0 && (
            <div className="flex flex-wrap gap-3">
              {data.images.map((img, i) => (
                <div key={i} className="relative group">
                  <img
                    src={`data:${img.mime_type};base64,${img.data}`}
                    alt={`Generated image ${i + 1}`}
                    className="max-w-xs max-h-64 rounded border border-border object-contain"
                  />
                  <a
                    href={`data:${img.mime_type};base64,${img.data}`}
                    download={`generated-image-${i + 1}.png`}
                    className="absolute bottom-1 right-1 flex items-center gap-1 bg-muted/70 hover:bg-muted/90 text-foreground text-xs px-2 py-1 rounded opacity-0 group-hover:opacity-100 transition-opacity"
                  >
                    <Download className="w-3 h-3" />
                    Save
                  </a>
                </div>
              ))}
            </div>
          )}
          <p className="text-xs text-muted-foreground italic">Prompt: {data.prompt}</p>
          {data.saved_paths && data.saved_paths.length > 0 && (
            <p className="text-xs text-muted-foreground">
              Saved to workspace: {data.saved_paths.join(', ')}
            </p>
          )}
        </div>
      )
    }

    case 'structured-data':
      return <StructuredDataRenderer content={parsedContent.content} metadata={parsedContent.metadata} />
      
    case 'error':
      return (
        <div className="text-sm text-red-700 dark:text-red-300 bg-red-50 dark:bg-red-900/20 p-3 rounded-md">
          <div className="flex items-center gap-2 mb-2">
            <AlertCircle className="w-4 h-4" />
            <span className="font-medium">Error Response</span>
          </div>
          <div className="text-xs whitespace-pre-wrap break-all">{parsedContent.content}</div>
        </div>
      )
      
    case 'code':
      return (
        <div className="space-y-2">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Code className="w-3 h-3" />
            <span>Code Response</span>
          </div>
          <div className="bg-muted p-3 rounded text-xs font-mono overflow-x-auto whitespace-pre-wrap break-all">
            {parsedContent.content}
          </div>
        </div>
      )
      
    case 'plain-text':
    default:
      return (
        <div className="text-sm text-foreground">
          <div className="flex items-center gap-2 mb-2">
            <FileText className="w-3 h-3" />
            <span className="font-medium">Text Response</span>
          </div>
          <div className="whitespace-pre-wrap break-words leading-relaxed">
            {parsedContent.content.split('\n').map((line, index) => (
              <div key={index} className="mb-1">
                {line || '\u00A0'}
              </div>
            ))}
          </div>
        </div>
      )
  }
} 
