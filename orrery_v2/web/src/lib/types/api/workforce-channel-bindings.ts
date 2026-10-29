export interface WorkforceChannelBindingsDTO {
  agent_profile_id: string;
  accounts: WorkforceChannelAccountDTO[];
}

export interface WorkforceChannelAccountDTO {
  channel_key: 'feishu';
  account_id: string;
  enabled: boolean;
  status: string;
  owner_agent_profile_id?: string;
  owner_display_name?: string;
  inbound_enabled: boolean;
  revision: number;
  created_at?: string;
  updated_at?: string;
}

export interface PutWorkforceChannelBindingRequest {
  inbound_enabled: boolean;
  expected_revision: number;
  expected_owner_agent_profile_id: string;
}

export interface DeleteWorkforceChannelBindingRequest {
  expected_revision: number;
  expected_owner_agent_profile_id: string;
}
