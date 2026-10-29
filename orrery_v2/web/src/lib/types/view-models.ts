export type * from './view-models/common';
export type * from './view-models/settings';
export type * from './view-models/resources';
export type * from './view-models/operations';
export type * from './view-models/workforce';
export type * from './view-models/mission';
export type * from './view-models/discussion-session';
export type * from './view-models/profile';

export interface WorkforceChannelBindingsViewModel {
  agentProfileId: string
  accounts: WorkforceChannelAccountViewModel[]
}

export interface WorkforceChannelAccountViewModel {
  key: string
  channelKey: 'feishu'
  accountId: string
  enabled: boolean
  status: string
  statusTone: 'neutral' | 'success' | 'warning' | 'danger' | 'info'
  ownerAgentProfileId: string
  ownerDisplayName: string
  inboundEnabled: boolean
  revision: number
  createdAt: string
  updatedAt: string
  ownedByTarget: boolean
}
