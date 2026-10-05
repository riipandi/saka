import { Hr } from 'react-email'
import { Alert, CardFooter, CardHeader, Text } from '../components'
import { sharedPreviewProps, sharedTemplateProps } from '../constants'
import { BaseTemplate } from '../layouts'

interface PasswordRemovedNoticeData {
  name: string
  email: string
}

interface PasswordRemovedNoticeProps {
  logoURL: string
  appName: string
  data: PasswordRemovedNoticeData
}

export const PasswordRemovedNotice = ({ logoURL, appName, data }: PasswordRemovedNoticeProps) => (
  <BaseTemplate logoURL={logoURL} appName={appName}>
    <CardHeader title='Your Password Was Removed' />
    <Hr style={{ marginTop: '16px' }} />
    <Text>Hello {data.name},</Text>

    <Text>
      The password for the account <strong>{data.email}</strong> was removed. You can still sign in
      with the other ways in the account keeps, such as a passkey or a linked sign-in provider.
    </Text>

    <Alert variant='warning' style={{ marginTop: '20px' }}>
      <strong>Security Notice:</strong> If you did <strong>not</strong> remove the password, someone
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

PasswordRemovedNotice.TemplateProps = {
  ...sharedTemplateProps,
  data: {
    name: '{{.Data.Name}}',
    email: '{{.Data.Email}}'
  }
}

PasswordRemovedNotice.PreviewProps = {
  ...sharedPreviewProps,
  data: {
    name: 'Robert Langdon',
    email: 'langdon@example.com'
  }
}

export default PasswordRemovedNotice
