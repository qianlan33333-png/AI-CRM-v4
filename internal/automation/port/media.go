package port

import mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"

// These aliases intentionally consume Media's stable, transaction-bound
// readers. Automation persists only opaque IDs and never reads Media tables.
type ImageMetadataReader = mediaport.ImageMetadataReader
type AttachmentMetadataReader = mediaport.AttachmentMetadataReader
type MiniProgramMetadataReader = mediaport.MiniProgramMetadataReader
type GroupInviteMetadataReader = mediaport.GroupInviteMetadataReader
