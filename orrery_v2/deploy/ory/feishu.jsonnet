local claims = std.extVar('claims');
local hasEmail = std.objectHas(claims, 'email') && claims.email != '';
local username = if hasEmail then claims.email else claims.sub;

{
  identity: {
    traits: {
      username: username,
      display_name:
        if std.objectHas(claims, 'name') && claims.name != ''
        then claims.name
        else username,
    } + if hasEmail then { email: claims.email } else {},
  },
}
