param([Parameter(Mandatory=$true)][string]$Path, [Parameter(Mandatory=$true)][string]$CertificateThumbprint)
$ErrorActionPreference = 'Stop'
$bridgeCertificate = Get-Item ('Cert:\CurrentUser\My\' + $CertificateThumbprint)
$bridgeSignature = Set-AuthenticodeSignature -FilePath $Path -Certificate $bridgeCertificate -HashAlgorithm SHA256 -TimestampServer 'http://timestamp.digicert.com'
if ($bridgeSignature.Status -ne 'Valid') { throw ('Signing failed: ' + $bridgeSignature.StatusMessage) }
if ((Get-AuthenticodeSignature -FilePath $Path).Status -ne 'Valid') { throw 'Signature verification failed.' }
