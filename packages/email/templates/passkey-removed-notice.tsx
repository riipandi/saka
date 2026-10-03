import { Hr } from 'react-email'
import { Alert, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface PasskeyNoticeData {
  name: string
  email: string
  credentialName: string
}

interface PasskeyNoticeProps {
  logoURL: string
  appName: string
  data: PasskeyNoticeData
}

export const PasskeyRemovedNotice = ({ logoURL, appName, data }: PasskeyNoticeProps) => (
  <BaseTemplate logoURL={logoURL} appName={appName}>
    <CardHeader title='A Passkey Was Removed' />
    <Hr style={{ marginTop: '16px' }} />
    <Text>Hello {data.name},</Text>

    <Text>
      The passkey{data.credentialName ? ` named “${data.credentialName}”` : ''} was removed from the
      account <strong>{data.email}</strong>. It can no longer sign in.
    </Text>

    <Alert variant='warning' style={{ marginTop: '20px' }}>
      <strong>Security Notice:</strong> If you did <strong>not</strong> remove this passkey, someone
      may have access to the account. Please contact support immediately.
    </Alert>

    <CardFooter>
      <Text size='sm' style={{ marginBottom: '4px' }}>
        You&apos;re receiving this email because you have an account in {appName}. <br />
        If you are not sure why you&apos;re receiving this, please contact us.
      </Text>
    </CardFooter>
  </BaseTemplate>
)

PasskeyRemovedNotice.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    email: '{{.Data.Email}}',
    credentialName: '{{.Data.CredentialName}}'
  }
}

PasskeyRemovedNotice.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Robert Langdon',
    email: 'langdon@example.com',
    credentialName: "Sophie's Laptop"
  }
}

export default PasskeyRemovedNotice
