import { Type } from "."

export enum Group {
  SINGLE = 0,
  SITE = 1,
  STYLE = 2,
  PREVIEW = 3,
  GLOBAL = 4,
  ARIA2 = 5,
  INDEX = 6,
  SSO = 7,
  LDAP = 8,
  S3 = 9,
  FTP = 10,
  TRAFFIC = 11,
  MEDIA = 12,
  TRANSCODE = 13,
}
export enum Flag {
  PUBLIC,
  PRIVATE,
  READONLY,
  DEPRECATED,
}

export interface SettingItem {
  key: string
  value: string
  type: Type
  help: string
  options?: string
  group: Group
  flag: Flag
}
