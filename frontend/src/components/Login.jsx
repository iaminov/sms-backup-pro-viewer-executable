import { useEffect } from 'react'
import { useNavigate } from 'react-router'

function Login() {
  const navigate = useNavigate()

  useEffect(() => {
    navigate('/', { replace: true })
  }, [navigate])

  return null
}

export default Login
