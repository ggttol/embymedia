export interface DiskMeta {
  label: string
  color: string
}

export const DISK_MAP: Record<string, DiskMeta> = {
  quark: { label: "夸克", color: "var(--disk-quark)" },
  baidu: { label: "百度网盘", color: "var(--disk-baidu)" },
  aliyun: { label: "阿里云盘", color: "var(--disk-aliyun)" },
  xunlei: { label: "迅雷", color: "var(--disk-xunlei)" },
  uc: { label: "UC 网盘", color: "var(--disk-uc)" },
  115: { label: "115", color: "var(--disk-115)" },
  123: { label: "123 云盘", color: "var(--disk-123)" },
  tianyi: { label: "天翼云盘", color: "var(--disk-tianyi)" },
  mobile: { label: "移动云盘", color: "var(--disk-mobile)" },
  pikpak: { label: "PikPak", color: "var(--disk-pikpak)" },
  guangya: { label: "光雅", color: "var(--disk-guangya)" },
  magnet: { label: "磁力链接", color: "var(--disk-magnet)" },
}

export const DEFAULT_DISK_COLOR = "var(--disk-other)"

export function getDiskLabel(diskType: string): string {
  return DISK_MAP[diskType]?.label ?? diskType
}

export function getDiskColor(diskType: string): string {
  return DISK_MAP[diskType]?.color ?? DEFAULT_DISK_COLOR
}

export const HEALTH_MAP: Record<string, { label: string; tone: string; icon: string }> = {
  valid: { label: "有效", tone: "ok", icon: "●" },
  invalid: { label: "失效", tone: "bad", icon: "×" },
  unknown: { label: "未检测", tone: "faint", icon: "○" },
  unsupported: { label: "免检/外部", tone: "faint", icon: "○" },
  checking: { label: "检测中", tone: "warn", icon: "◌" },
}

export function getHealthLabel(status: string): string {
  return HEALTH_MAP[status]?.label ?? status
}
