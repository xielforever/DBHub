import {iamUiEnabled} from './iam-mode'

export const workspaceGovernanceActions = ['space.roles.manage', 'space.assign.manage', 'space.members.manage', 'space.policy.manage'] as const

export function requiresActionChecks(snapshot?:{target:boolean;rollback_guard?:boolean;denied_actions?:string[]}|null):boolean{return !!snapshot&&(snapshot.target||snapshot.rollback_guard===true||!!snapshot.denied_actions?.length)}
export function featureAllowed(user:{can:(action:string)=>boolean;effective?:{snapshot:{target:boolean;rollback_guard?:boolean;denied_actions?:string[]}}|null},action:string,legacy:boolean):boolean{return iamUiEnabled&&requiresActionChecks(user.effective?.snapshot)?user.can(action):legacy}
